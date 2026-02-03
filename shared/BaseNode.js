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
    //   treeId: injection-1              // ID dell'albero a cui appartiene il nodo
    //   layer: 0                         // Livello dell'albero
    // 
    // === RTP PORTS ===
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
    this.parents = [];
    this.children = [];

    this.isStopping = false;

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
    this.subscriber = this.redis.duplicate({
      enableReadyCheck: false,
      maxRetriesPerRequest: null
    });

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
    // Subscribe a canali
    const channels = [
      `node:${this.nodeId}:topology`, // Eventi diretti a questo nodo
      `topology:global`,              // Reset globali
      `node:${this.nodeId}:sessions`, // Sessioni che questo nodo deve gestire
      `sessions:global`               // Eventi distruzione globale
    ];

    await this.subscriber.subscribe(...channels);

    // Handler messaggi
    this.subscriber.on('message', async (channel, message) => {
      try {
        if (channel.endsWith(':sessions') || channel === 'sessions:global') {
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
    await this.redis.hset(`node:${this.nodeId}`, {                               //  hset setta come hash redis e non come json 
      nodeId: this.nodeId,                                                      //  dovrebbe essere un'azione atomica quindi piu performante (boh)
      type: this.nodeType,
      host: this.host,
      port: this.port,
      audioPort: this.rtp.audioPort,
      videoPort: this.rtp.videoPort,
      status: 'active',
      created: Date.now()
    });

    await this.redis.expire(`node:${this.nodeId}`, 600);
    // Registra nodo nel tree
    const setKey = `pool:${this.nodeType}`; // pool:injection, pool:relay, pool:egress
    await this.redis.sadd(setKey, this.nodeId);
    console.log(`[${this.nodeId}] Registered in pool (${setKey})`);
  }

  async unregisterNode() {
    await this.redis.del(`node:${this.nodeId}`);
    const setKey = `pool:${this.nodeType}`;
    await this.redis.srem(setKey, this.nodeId);
  }

  // TOPOLOGY

  // Handler eventi topologia
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
      case 'parent-added':
        if (event.nodeId === this.nodeId) {
          await this.handleParentAdded(event.parentId);
        }
        break;

      case 'parent-removed':
        if (event.nodeId === this.nodeId) {
          await this.handleParentRemoved(event.parentId);
        } break;

      case 'child-added':
        if (event.nodeId === this.nodeId) {
          await this.handleChildAddedEvent(event);
        }
        break;

      case 'child-removed':
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
      const parentsKey = `node:${this.nodeId}:parents`;
      const childrenKey = `node:${this.nodeId}:children`;
      // relay/egress
      const [newParents, newChildren] = await Promise.all([
        this.redis.smembers(parentsKey),
        this.redis.smembers(childrenKey)
      ]);
      if (this.nodeType !== 'injection') {

        // Confronta con current
        const currentSet = new Set(this.parents);
        const newSet = new Set(newParents);

        // Find added/removed
        const added = [...newSet].filter(p => !currentSet.has(p));
        const removed = [...currentSet].filter(p => !newSet.has(p));

        if (added.length > 0 || removed.length > 0) {
          console.log(`[${this.nodeId}] Parents changed: +${added.length} -${removed.length}`);

          this.parents = newParents;

          // Notifica add/remove separatamente
          for (const parentId of added) {
            await this.onParentAdded(parentId);
          }
          for (const parentId of removed) {
            await this.onParentRemoved(parentId);
          }
        }
      }

      // injection/relay
      if (this.nodeType !== 'egress') {

        const currentSet = new Set(this.children);
        const newSet = new Set(newChildren);

        const added = [...newSet].filter(c => !currentSet.has(c));
        const removed = [...currentSet].filter(c => !newSet.has(c));

        if (added.length > 0 || removed.length > 0) {
          console.log(`[${this.nodeId}] Children changed: +${added.length} -${removed.length}`);
          this.children = newChildren;
          await this.onChildrenChanged(added, removed);
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
    const eventType = event.type ? event.type.trim() : "";
    console.log(`[${this.nodeId}] Received session event: ${eventType} for session: ${event.sessionId}`);


    switch (eventType) {
      case 'session-created':
        await this.onSessionCreated(event);
        break;

      case 'session-destroyed':
        await this.onSessionDestroyed(event);
        break;

      case 'route-added':
        await this.onRouteAdded(event);
        break;

      case 'route-removed':
        await this.onRouteRemoved(event);
        break;

      default:
        console.warn(`[${this.nodeId}] Unhandled event type: "${eventType}" (raw: ${event.type})`);
    }
  }


  // Handler parent-changed
  async handleParentAdded(parentId) {
    // Verifica se già presente
    if (this.parents.includes(parentId)) {
      console.log(`[${this.nodeId}] Parent ${parentId} already exists, ignoring`);
      return;
    }

    console.log(`[${this.nodeId}] Adding parent: ${parentId}`);
    this.parents.push(parentId);

    // Hook per sottoclasse
    await this.onParentAdded(parentId);
  }

  async handleParentRemoved(parentId) {
    const index = this.parents.indexOf(parentId);
    if (index === -1) {
      console.log(`[${this.nodeId}] Parent ${parentId} not found, ignoring`);
      return;
    }

    console.log(`[${this.nodeId}] Removing parent: ${parentId}`);
    this.parents.splice(index, 1);

    // Hook per sottoclasse
    await this.onParentRemoved(parentId);
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

  async getParentsInfo() {
    if (!this.parents) return null;
    const parentsInfo = [];
    for (const parentId of this.parents) {
      const info = await this.getNodeInfo(parentId);
      if (info) parentsInfo.push(info);
    }
    return parentsInfo;
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
      let redisParents = null;
      let redisChildren = [];

      if (this.nodeType !== 'injection') {
        redisParents = await this.redis.smembers(`node:${this.nodeId}:parents`);
      }

      if (this.nodeType !== 'egress') {
        redisChildren = await this.redis.smembers(`node:${this.nodeId}:children`);
      }

      let desync = false;

      //  Gestione parent
      // Primo elemento del set
      const newParent = redisParents.length > 0 ? redisParents[0] : null;

      if (newParent !== this.parent) {
        console.log(`[${this.nodeId}] Parent changed: ${this.parent} -> ${newParent}`);
        const oldParent = this.parent;
        this.parent = newParent;

        if (newParent) {
          await this.onParentAdded(newParent);
        } else if (oldParent) {
          await this.onParentRemoved(oldParent);
        }
      }

      // Gestione children
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
        parents: this.parents,
        children: this.children
      });
    });

    // Current topology
    this.app.get('/topology', (req, res) => {
      res.json({
        nodeId: this.nodeId,
        nodeType: this.nodeType,
        parents: this.parents,
        children: this.children
      });
    });
    this.app.get('/metrics', async (req, res) => {
      try {
        const metrics = await this.getMetrics();
        res.json(metrics);
      } catch (error) {
        res.status(500).json({ error: error.message });
      }
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
        parents: this.parents,
        children: this.children
      },
      uptime: Math.floor(process.uptime()),
    };
  }

  async getMetrics() {
    return {
      nodeId: this.nodeId,
      nodeType: this.nodeType,
      timestamp: Date.now(),
      janus: null  // Override in injection/relay
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

  async onParentAdded(parentId) {
    // Override in relay/egress
  }
  async onParentRemoved(parentId) {
    // Override in relay/egress
  }


  async onChildrenChanged(added, removed) {
    // Override in injection/relay
  }

  async onSessionCreated(event) {
    // Override in relay/egress
  }

  async onSessionDestroyed(event) {
    // Override in relay/egress
  }

  async onRouteAdded(event) {
    // Override in relay
  }

  async onRouteRemoved(event) {
    // Override in relay
  }
}