import Redis from 'ioredis';
import express from 'express';

export class BaseNode {
  constructor(nodeId, nodeType, config) {

    this.nodeId = nodeId;
    this.nodeType = nodeType;
    this.config = config;


    // CONFIG STRUCTURE:
    // {
    //   // === NETWORK (OBBLIGATORIO PER TUTTI I NODI) ===
    //   nodeId: 'injection-1',           // Identificativo univoco nodo
    //   nodeType: 'injection',           // injection | relay | egress
    //   host: 'injection-1',             // Hostname/IP del nodo
    //   port: 7070,                      // Porta API REST
    //
    //   // === RTP PORTS ===
    //   rtp: {
    //     audioPort: 5000,              // Porta UDP per RTP audio
    //     videoPort: 5002               // Porta UDP per RTP video
    //   },
    //
    //   // === REDIS (OBBLIGATORIO PER TUTTI I NODI) ===
    //   redis: {
    //     host: 'redis',                 // Hostname Redis
    //     port: 6379,                    // Porta Redis
    //     password: 'pwd'       // Opzionale
    //   },
    //
    //   // === JANUS (SPECIFICO PER TIPO DI NODO) ===
    //   janus: {
    //     // SOLO PER INJECTION NODE:
    //     videoroom: {
    //       wsUrl: 'ws://janus-videoroom:8188'
    //     },
    //     
    //     // SOLO PER EGRESS NODE:
    //     streaming: {
    //       wsUrl: 'ws://janus-streaming:8188'
    //     }
    //   }
    //.  // === WHIP ===
    //     whip: {
    //       basePath: '/whip'    // opzionale
    //       token: 'verysecret'  // opzionale
    //       secret: 'adminpwd'   // opzionale
    //     }
    // }

    this.host = config.host || 'localhost';
    this.port = config.port || 7070;

    this.rtp = {
      audioPort: config.rtp.audioPort,
      videoPort: config.rtp.videoPort
    };

    // stato
    this.redis = null;
    this.app = express();
    this.server = null;
    this.pollTimer = null;

    // topology
    this.parent = null;
    this.children = [];

    this.isStopping = false;
    this.pipelineHealth = null;

    this.app.use(express.json());
  }

  async initialize() {
    console.log(`[${this.nodeId}] Inizializing node ${this.nodeType}...`);

    await this.connectRedis(); // 1. Connette a Redis
    await this.registerNode(); // 2. Registra nodo in Redis
    this.setupBaseAPI();       // 3. Configura API endpoints 
    await this.onInitialize(); // 4. inizializzazione

    return true;
  }

  async start() {
    this.server = this.app.listen(this.port, () => {
      console.log(`[${this.nodeId}] API in ascolto su porta :${this.port}`);
    });

    this.startPolling();
    await this.updateTopology();
    await this.onStart();            // Hook per avvio specifico per ogni nodo (override)


    console.log(`[${this.nodeId}]starting node...`);
  }

  async stop() {
    this.isStopping = true;
    console.log(`[${this.nodeId}] Stopping node...`);

    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }

    await this.onStop();             // Hook per avvio specifico per ogni nodo (override)
    await this.unregisterNode();

    if (this.server) this.server.close();
    if (this.redis) this.redis.disconnect();
    console.log(`[${this.nodeId}] Node stopped`);
  }

  // ============ REDIS ============

  async connectRedis() {
    this.redis = new Redis({
      host: this.config.redis.host,
      port: this.config.redis.port,
      password: this.config.redis.password,
      retryStrategy: (times) => Math.min(times * 50, 2000)  // strategia base di ripetizione di redis
    });

    await this.redis.ping();
    console.log(`[${this.nodeId}] Redis connected`);
  }

  async registerNode() {
    await this.redis.hset(`node:${this.nodeId}`, {          //  hset setta come hash redis e non come json 
      id: this.nodeId,                                      //  dovrebbe essere un'azione atomica quindi piu performante (boh)
      type: this.nodeType,
      host: this.host,
      port: this.port,
      audioPort: this.rtp.audioPort,
      videoPort: this.rtp.videoPort,
      status: 'active',
      created: Date.now()
    });

    await this.redis.expire(`node:${this.nodeId}`, 60);

    console.log(`[${this.nodeId}] Registered`);
  }

  async unregisterNode() {
    await this.redis.del(`node:${this.nodeId}`);
    // await this.redis.del(`children:${this.nodeId}`); // forse dovrebbe farlo il controller questo
    // await this.redis.del(`parent:${this.nodeId}`);
  }

  // ============ TOPOLOGY ============

  async updateTopology() {
    try {
      // relay/egress
      if (this.nodeType !== 'injection') {
        const parentId = await this.redis.get(`parent:${this.nodeId}`);  // get e non hget per il controller farà solo set visto che è una stringa unica no?
        if (parentId !== this.parent) {
          const oldParent = this.parent;
          this.parent = parentId;
          console.log(`[${this.nodeId}] Parent changed: ${oldParent} → ${parentId}`);

          await this.onParentChanged(oldParent, parentId);
        }
      }

      // injection/relay
      if (this.nodeType !== 'egress') {
        const newChildren = await this.redis.smembers(`children:${this.nodeId}`);   // smember = dammi il set di membri.

        const currentSet = new Set(this.children);
        const newSet = new Set(newChildren);

        if (currentSet.size !== newSet.size ||
          ![...currentSet].every(child => newSet.has(child))) {

          const added = newChildren.filter(child => !currentSet.has(child));
          const removed = this.children.filter(child => !newSet.has(child));

          this.children = newChildren;
          await this.onChildrenChanged(added, removed);

          console.log(`[${this.nodeId}] Children: +${added.length} -${removed.length}`);
        }
      }

    } catch (error) {
      console.error(`[${this.nodeId}] Topology update error:`, error.message);
    }
  }

  async getNodeInfo(nodeId) {
    const data = await this.redis.hgetall(`node:${nodeId}`);
    if (!data || Object.keys(data).length === 0) return null;

    return {
      ...data,
      port: parseInt(data.port),
      audioPort: parseInt(data.audioPort),
      videoPort: parseInt(data.videoPort),
      created: parseInt(data.created)
    };
  }

  async getParentInfo() {
    if (!this.parent) return null;
    return await this.getNodeInfo(this.parent);
  }

  async getChildrenInfo() {
    const childrenInfo = [];
    for (const childId of this.children) {
      const info = await this.getNodeInfo(childId);
      if (info) childrenInfo.push(info);
    }
    return childrenInfo;
  }

  // ============ POLLING ============

  startPolling(interval = 30000) {
    this.pollTimer = setInterval(async () => {
      await this.updateTopology();
      await this.refreshNodeTTL();
    }, interval);

    console.log(`[${this.nodeId}] Polling started (${interval}ms)`);
  }

  async refreshNodeTTL() {
    await this.redis.expire(`node:${this.nodeId}`, 60);
  }

  // ============ GSTREAMER HELPER ============

  //@param {string} 'audio' o 'video'
  //@param {ChildProcess} Pipeline
  //@param {Function} onRestart - Callback per auto-restart

  _attachPipelineHandlers(type, process, onRestart) {
    // stdout
    process.stdout.on('data', (data) => {
      const msg = data.toString().trim();
      if (msg) {
        console.log(`[${this.nodeId}] ${type} stdout: ${msg}`);
      }
    });

    // stderr
    process.stderr.on('data', (data) => {
      const msg = data.toString().trim();

      if (msg &&
        !msg.includes('Setting pipeline to PAUSED') &&
        !msg.includes('Setting pipeline to PLAYING') &&
        !msg.includes('Prerolled') &&
        !msg.includes('New clock')) {
        console.log(`[${this.nodeId}] ${type} stderr: ${msg}`);
      }
    });

    // close
    process.on('close', (code) => {
      this.pipelineHealth[type].running = false;
      console.log(`[${this.nodeId}] ${type} pipeline exited with code ${code}`);

      if (code !== 0) {
        // Pipeline crash
        const errorMsg = `${type} pipeline crashed with code ${code}`;
        console.error(`[${this.nodeId}] ${errorMsg}`);
        this.pipelineHealth[type].lastError = errorMsg;

        // Auto-restart
        if (!this.isStopping && onRestart) {
          console.log(`[${this.nodeId}] Restarting ${type} pipeline in 5 seconds...`);
          setTimeout(() => {
            if (!this.isStopping) {
              onRestart();
            }
          }, 5000);
        }
      }
    });

    // spawn error
    process.on('error', (err) => {
      console.error(`[${this.nodeId}] ${type} pipeline spawn error:`, err.message);
      this.pipelineHealth[type].running = false;
      this.pipelineHealth[type].lastError = err.message;
    });
  }

  // ============ API ============

  setupBaseAPI() {
    this.app.get('/status', async (req, res) => {
      const status = await this.getStatus();
      res.json(status);
    });

    // Force topology refresh
    this.app.post('/refresh', async (req, res) => {
      console.log(`[${this.nodeId}] Manual refresh`);
      await this.updateTopology();
      res.json({
        nodeId: this.nodeId,
        parent: this.parent,
        children: this.children
      });
    });

    // Current topology
    this.app.get('/topology', (req, res) => {
      res.json({
        nodeId: this.nodeId,
        nodeType: this.nodeType,
        parent: this.parent,
        children: this.children
      });
    });
  }

  async getStatus() {
    return {
      healthy: true,
      nodeId: this.nodeId,
      nodeType: this.nodeType,
      network: {
        host: this.host,
        apiPort: this.port,
        audioPort: this.rtp.audioPort,
        videoPort: this.rtp.videoPort
      },
      topology: {
        parent: this.parent,
        children: this.children
      },
      uptime: Math.floor(process.uptime()),
    };
  }

  async onInitialize() {
    // Override
  }

  async onStart() {
    // Override
  }

  async onStop() {
    // Override
  }

  async onParentChanged(oldParent, newParent) {
    // Override in relay/egress
  }

  async onChildrenChanged(added, removed) {
    // Override in injection/relay
  }

}