import Redis from 'ioredis';
import express from 'express';

export class BaseNode {
  constructor(nodeId, nodeType, config) {

    this.nodeId = nodeId;
    this.nodeType = nodeType;
    this.config = config;


    // CONFIG STRUCTURE:
    // {
    //   
    //   nodeId: 'injection-1',           // Identificativo nodo
    //   nodeType: 'injection',           // injection | relay | egress
    //   host: 'injection-1',             // Hostname/IP del nodo
    //   port: 7070,                      // Porta API REST
    //   treeId: injection-1              // Un nodo di ingresso per ogni albero, a questo punto lo utilizziamo come id del tree 
    //
    //   // === RTP PORTS ===
    //   rtp: {
    //     audioPort: 5000,              // Porta UDP per RTP audio
    //     videoPort: 5002               // Porta UDP per RTP video
    //   },
    //
    //   // REDIS (OBBLIGATORIO PER TUTTI I NODI)
    //   redis: {
    //     host: 'redis',                 // Hostname Redis
    //     port: 6379,                    // Porta Redis
    //     password: 'pwd'       // Opzionale
    //   },
    //
    //   // JANUS (SPECIFICO PER TIPO DI NODO)
    //   janus: {
    //     // SOLO PER INJECTION NODE:
    //     videoroom: {
    //       wsUrl: 'ws://janus-videoroom:8188',
    //       apiSecret: 'janusoverlord' o 'janusrocks',
    //       roomSecret: 'adminpwd'             // Secret per modificare room
    //     },
    //     
    //     // SOLO PER EGRESS NODE:
    //     streaming: {
    //       wsUrl: 'ws://janus-streaming:8188',
    //       apiSecret: 'janusoverlord' o 'janusrocks',
    //       mountpointSecret: 'adminpwd'             // Secret per modificare mountpoint
    //     }
    //   }
    //   // WHIP SERVER (SOLO INJECTION NODE)
    //   whip: {
    //     basePath: '/whip',             // Base path per WHIP endpoints
    //     token: 'verysecret'           // Token autenticazione clients            
    //   },
    //
    //   // WHEP SERVER (SOLO EGRESS NODE)
    //   whep: {
    //     basePath: '/whep',             // Base path per WHEP endpoints
    //     token: 'verysecret'            // Token autenticazione viewer         
    //   },
    //
    //   // PORT POOL (SOLO EGRESS NODE)
    //   portPoolBase: 6000,              // Prima porta del pool
    //   portPoolSize: 100                // Numero porte disponibili
    // }

    this.treeId = config.treeId || (nodeType === 'injection' ? nodeId : null);
    this.host = config.host || 'localhost';
    this.port = config.port || 7070;

    this.rtp = {
      audioPort: config.rtp.audioPort,
      videoPort: config.rtp.videoPort
    };

    // stato
    this.redis = null;
    this.subscriber = null;  // pub/sub
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
    await this.connectRedis(); // Connette a Redis
    await this.registerNode(); // Registra nodo in Redis
    this.setupBaseAPI();       // Configura API endpoints 
    await this.onInitialize(); // inizializzazione

    return true;
  }

  async start() {
    this.server = this.app.listen(this.port, () => {
      console.log(`[${this.nodeId}] API port : ${this.port}`);
    });

    await this.updateTopology();
    await this.setupPubSub();       // Subscribe eventi (dopo sync)
    this.startPeriodicSync();       // sync ogni 5/10 min
    await this.onStart();           // Hook per avvio specifico per ogni nodo


    // console.log(`[${this.nodeId}] starting node...`);
  }

  async stop() {
    this.isStopping = true;
    //console.log(`[${this.nodeId}] Stopping node...`);

    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }

    if (this.subscriber) {
      try {
        await this.subscriber.unsubscribe();
        await this.subscriber.quit();
        console.log(`[${this.nodeId}] Subscriber disconnected`);
      } catch (err) {
        console.error(`[${this.nodeId}] Error disconnecting subscriber:`, err.message);
      }
    }

    await this.onStop();             // Hook per avvio specifico per ogni nodo
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
    //console.log(`[${this.nodeId}] Redis connected`);

    this.redis.on('error', (err) => {
      console.error(`[${this.nodeId}] Redis error:`, err.message);
    });

    // Client Pub/Sub
    this.subscriber = this.redis.duplicate();

    this.subscriber.on('error', (err) => {
      console.error(`[${this.nodeId}] Subscriber error:`, err.message);
    });

    // Handler reconnect
    this.subscriber.on('ready', async () => {
      console.log(`[${this.nodeId}] Subscriber reconnected`);
      // Re-subscribe automatico gestito da ioredis
      // sync
      await this.updateTopology();
    });

  }
  // Setup Pub/Sub
  async setupPubSub() {
    // Subscribe a canali topology
    const channels = [
      `topology:${this.nodeId}`,
      `topology:${this.treeId}`,
    ];

    if (this.nodeType === 'injection' || this.nodeType === 'egress') {
      channels.push(`sessions:node:${this.nodeId}`);
    }
    if (this.nodeType === 'egress') {
      channels.push(`sessions:tree:${this.treeId}`);
    }

    await this.subscriber.subscribe(...channels);

    // Handler messaggi
    this.subscriber.on('message', async (channel, message) => {
      try {
        if (channel.startsWith('sessions:')) {
          await this.handleSessionEvent(channel, message);
        } else {
          await this.handleTopologyEvent(channel, message);
        }
      } catch (err) {
        console.error(`[${this.nodeId}] Error handling event:`, err.message);
      }
    });
  }

  async registerNode() {
    await this.redis.hset(`node:${this.nodeId}`, {          //  hset setta come hash redis e non come json 
      nodeId: this.nodeId,                                  //  dovrebbe essere un'azione atomica quindi piu performante (boh)
      type: this.nodeType,
      treeId: this.treeId,
      host: this.host,
      port: this.port,
      audioPort: this.rtp.audioPort,
      videoPort: this.rtp.videoPort,
      status: 'active',
      created: Date.now()
    });

    await this.redis.expire(`node:${this.nodeId}`, 600);

    console.log(`[${this.nodeId}] Registered`);
  }

  async unregisterNode() {
    await this.redis.del(`node:${this.nodeId}`);
    // await this.redis.del(`children:${this.nodeId}`); // forse dovrebbe farlo il controller questo
    // await this.redis.del(`parent:${this.nodeId}`);
  }

  // TOPOLOGY

  // Handler eventi Pub/Sub
  async handleTopologyEvent(channel, message) {
    let event;

    try {
      event = JSON.parse(message);
    } catch (err) {
      console.error(`[${this.nodeId}] Failed to parse event:`, message);
      return;
    }

    console.log(`[${this.nodeId}] Event [${channel}]: ${event.type}`);

    switch (event.type) {
      case 'parent-changed':
        // Solo se evento per questo nodo
        if (event.nodeId === this.nodeId) {
          await this.handleParentChangedEvent(event);
        }
        break;

      case 'child-added':
        // Solo se evento per questo nodo
        if (event.nodeId === this.nodeId) {
          await this.handleChildAddedEvent(event);
        }
        break;

      case 'child-removed':
        // Solo se evento per questo nodo
        if (event.nodeId === this.nodeId) {
          await this.handleChildRemovedEvent(event);
        }
        break;

      case 'topology-reset':
        // Evento globale: full sync
        console.log(`[${this.nodeId}] Topology reset requested`);
        await this.updateTopology();
        break;

      default:
        console.log(`[${this.nodeId}] Unknown event type: ${event.type}`);
    }
  }

  async updateTopology() {
    try {
      // relay/egress
      if (this.nodeType !== 'injection') {
        const parentId = await this.redis.get(`parent:${this.nodeId}`);  // get e non hget per il controller farà solo set visto che è una stringa unica
        if (parentId !== this.parent) {
          const oldParent = this.parent;
          this.parent = parentId;
          console.log(`[${this.nodeId}] Parent changed: ${oldParent} -> ${parentId}`);

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
  // handler eventi sessione
  async handleSessionEvent(channel, message) {
    let event;
    try {
      event = JSON.parse(message);
    } catch (err) {
      console.error(`[${this.nodeId}] Failed to parse session event:`, message);
      return;
    }

    console.log(`[${this.nodeId}] Session event [${channel}]: ${event.type}`);

    switch (event.type) {
      case 'session-created':
        await this.onSessionCreated(event);
        break;

      case 'session-destroyed':
        await this.onSessionDestroyed(event);
        break;

      default:
        console.log(`[${this.nodeId}] Unknown session event: ${event.type}`);
    }
  }


  // Handler parent-changed
  async handleParentChangedEvent(event) {
    // Solo per relay/egress
    if (this.nodeType === 'injection') return;

    const oldParent = this.parent;
    const newParent = event.newParent;

    // Update cache locale
    this.parent = newParent;

    console.log(`[${this.nodeId}] Parent changed: ${oldParent} -> ${newParent}`);

    // Chiama hook
    await this.onParentChanged(oldParent, newParent);
  }

  // Handler child-added
  async handleChildAddedEvent(event) {
    // Solo per injection/relay
    if (this.nodeType === 'egress') return;

    const childId = event.childId;

    // Check duplicato
    if (this.children.includes(childId)) {
      console.log(`[${this.nodeId}] Child ${childId} already in cache, skipping`);
      return;
    }

    // Update cache locale
    this.children.push(childId);

    console.log(`[${this.nodeId}] Child added: ${childId}`);

    // Chiama hook
    await this.onChildrenChanged([childId], []);
  }

  // Handler child-removed
  async handleChildRemovedEvent(event) {
    // Solo per injection/relay
    if (this.nodeType === 'egress') return;

    const childId = event.childId;

    // Check se esiste
    const index = this.children.indexOf(childId);
    if (index === -1) {
      console.log(`[${this.nodeId}] Child ${childId} not in cache, skipping`);
      return;
    }

    // Update cache locale
    this.children.splice(index, 1);

    console.log(`[${this.nodeId}] Child removed: ${childId}`);

    // Chiama hook esistente
    await this.onChildrenChanged([], [childId]);
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

  // POLLING
  startPeriodicSync(interval = 5 * 60 * 1000) {  // 5 minuti
    this.pollTimer = setInterval(async () => {
      if (!this.isStopping) {
        console.log(`[${this.nodeId}] Periodic sync (fallback)`);
        await this.periodicSync();
        await this.refreshNodeTTL();
      }
    }, interval);

    console.log(`[${this.nodeId}] Periodic sync started (${interval}ms)`);
  }

  async refreshNodeTTL() {
    await this.redis.expire(`node:${this.nodeId}`, 600);
  }

  async periodicSync() {
    try {
      // Leggi stato da Redis
      let redisParent = null;
      let redisChildren = [];

      if (this.nodeType !== 'injection') {
        redisParent = await this.redis.get(`parent:${this.nodeId}`);
      }

      if (this.nodeType !== 'egress') {
        redisChildren = await this.redis.smembers(`children:${this.nodeId}`);
      }

      // Confronta con cache locale
      let desync = false;

      // Check parent
      if (this.nodeType !== 'injection' && redisParent !== this.parent) {
        console.warn(`[${this.nodeId}] DESYNC parent: cache=${this.parent} redis=${redisParent}`);
        this.parent = redisParent;
        await this.onParentChanged(null, redisParent);
        desync = true;
      }

      // Check children
      if (this.nodeType !== 'egress') {
        const redisSet = new Set(redisChildren);
        const localSet = new Set(this.children);

        const added = [...redisSet].filter(c => !localSet.has(c));
        const removed = [...localSet].filter(c => !redisSet.has(c));

        if (added.length > 0 || removed.length > 0) {
          console.warn(`[${this.nodeId}] DESYNC children: +${added.length} -${removed.length}`);
          this.children = redisChildren;
          await this.onChildrenChanged(added, removed);
          desync = true;
        }
      }

      if (desync) {
        console.warn(`[${this.nodeId}] Desync corrected via periodic sync`);
      }

    } catch (error) {
      console.error(`[${this.nodeId}] Periodic sync error:`, error.message);
    }
  }
  // API

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

  async onSessionCreated(event) {
    // Override in egress
  }

  async onSessionDestroyed(event) {
    // Override in egress
  }

}