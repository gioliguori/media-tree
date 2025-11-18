import { BaseNode } from '../shared/BaseNode.js';
import { PortPool } from '../shared/PortPool.js';
import { saveMountpointToRedis, deactivateMountpointInRedis, getMountpointInfo, getAllMountpointsInfo } from './mountpoint-utils.js';
import { connectToJanusStreaming, createJanusMountpoint, destroyJanusMountpoint } from './janus-streaming-utils.js';
import { EgressForwarderManager } from './EgressForwarderManager.js';

import { JanusWhepServer } from 'janus-whep-server';
import express from 'express';

export class EgressNode extends BaseNode {
    constructor(config) {
        super(config.nodeId, 'egress', config);

        // WHEP Server
        this.whepServer = null;
        this.janusUrl = config.janus.streaming.wsUrl;
        this.janusApiSecret = config.janus.streaming.apiSecret || null;
        this.whepBasePath = config.whep?.basePath || '/whep';
        this.whepToken = config.whep?.token || 'verysecret';
        this.mountpointSecret = config.janus.streaming.mountpointSecret || 'adminpwd';

        // Janus connection pool
        this.janusConnection = null;
        this.janusSession = null;
        this.janusStreaming = null;

        // RTP destination host (deriva da janusUrl)
        this.rtpDestinationHost = new URL(this.janusUrl).hostname;
        // console.log(`[${this.nodeId}] RTP destination host: ${this.rtpDestinationHost}`);

        // PortPool per allocare porte OUTPUT verso Janus
        this.portPool = new PortPool(
            config.portPoolBase || 6000,
            config.portPoolSize || 100
        );

        // Mountpoints attivi
        this.mountpoints = new Map(); // sessionId -> mountpointData
        // mountpointData = {
        //   sessionId: string,              // ID sessione upstream (da InjectionNode)
        //   mountpointId: number,           // ID mountpoint Janus (= roomId)
        //   audioSsrc: number,              // SSRC audio per demux
        //   videoSsrc: number,              // SSRC video per demux
        //   janusAudioPort: number,         // Porta allocata dal PortPool per audio
        //   janusVideoPort: number,         // Porta allocata dal PortPool per video
        //   endpoint: whepEndpoint,         // Oggetto WHEP endpoint per viewers
        //   active: boolean,                
        //   createdAt: timestamp            
        // }

        // Lock per operazioni per sessione
        this.operationLocks = new Map();


        // ForwarderManager per gestire processo C
        this.forwarder = new EgressForwarderManager({
            nodeId: this.nodeId,
            rtpAudioPort: String(this.rtp.audioPort),
            rtpVideoPort: String(this.rtp.videoPort),
            rtpDestinationHost: this.rtpDestinationHost,
            syncCallback: this.syncForwarderState.bind(this)
        });
    }

    async onInitialize() {
        console.log(`[${this.nodeId}] Initializing WHEP server...`);

        // Connessione a Janus per gestione mountpoint
        const { connection, session, streaming } = await connectToJanusStreaming(this.nodeId, {
            wsUrl: this.janusUrl,
            apiSecret: this.janusApiSecret
        });

        this.janusConnection = connection;
        this.janusSession = session;
        this.janusStreaming = streaming;

        // Inizializza JanusWhepServer
        this.whepServer = new JanusWhepServer({
            janus: { address: this.janusUrl },
            rest: { app: this.app, basePath: this.whepBasePath }
        });

        // file statici test visivo
        this.app.use(express.static('web'));

        console.log(`[${this.nodeId}] WHEP server initialized Janus: ${this.janusUrl}`);
    }

    async onStart() {
        console.log(`[${this.nodeId}] Starting WHEP server...`);

        await this.whepServer.start();

        console.log(`[${this.nodeId}] WHEP server listening on ${this.whepBasePath}`);

        // AVVIA PROCESSO C
        await this.forwarder.startForwarder();

        // necessaria quando aggiungiamo un nodo a un albero con sessioni attive 
        await this.discoverExistingSessions();
        // a questo punto con discoverExistingSessions() syncForwarder() potrebbe essere ridondante 
        // await this.forwarder.syncForwarder();
        // paused -> playing, altrimenti problemi quando facciamo recovery 
        await this.forwarder.startPipelines();

        // PING
        this.forwarder.startHealthCheck();
        console.log(`[${this.nodeId}] Egress node ready`);
    }

    async onStop() {
        console.log(`[${this.nodeId}] Stopping egress node...`);

        // Ferma health check
        this.forwarder.stopHealthCheck();

        // Distruggi tutti i mountpoint 
        const sessionIds = Array.from(this.mountpoints.keys());
        console.log(`[${this.nodeId}] Destroying ${sessionIds.length} active mountpoints...`);

        for (const sessionId of sessionIds) {
            try {
                await this.destroyMountpoint(sessionId);
            } catch (error) {
                console.error(`[${this.nodeId}] Error destroying mountpoint ${sessionId}:`, error.message);
            }
        }

        // chiudi socket, termina processo C
        await this.forwarder.shutdown();

        // Stop WHEP server
        if (this.whepServer) {
            try {
                await this.whepServer.stop();
                console.log(`[${this.nodeId}] WHEP server stopped`);
            } catch (error) {
                console.error(`[${this.nodeId}] Error stopping WHEP server:`, error.message);
            }
        }

        // Disconnect Janus
        if (this.janusConnection) {
            try {
                await this.janusConnection.close();
                console.log(`[${this.nodeId}] Janus connection closed`);
            } catch (error) {
                console.error(`[${this.nodeId}] Error closing Janus connection:`, error.message);
            }
        }

        console.log(`[${this.nodeId}] Egress node stopped`);
    }


    // SYNC CALLBACK per ForwarderManager
    // forwarderSessions = sessionId presenti nel forwarder C
    async syncForwarderState(forwarderSessions) {
        // Crea Set di sessionId (da memoria)
        const sessions = new Set(this.mountpoints.keys());

        // ADD missing mountpoints
        for (const sessionId of sessions) {
            if (!forwarderSessions.has(sessionId)) {
                const mountpoint = this.mountpoints.get(sessionId);
                console.log(`[${this.nodeId}] adding missing mountpoint ${sessionId} `);

                try {
                    await this.forwarder.sendCommand(
                        `ADD ${sessionId} ${mountpoint.audioSsrc} ${mountpoint.videoSsrc} ` +
                        `${mountpoint.janusAudioPort} ${mountpoint.janusVideoPort} `
                    );
                } catch (err) {
                    console.error(`[${this.nodeId}] Failed to add ${sessionId}: `, err.message);
                }
            }
        }

        // REMOVE extra mountpoints
        for (const sessionId of forwarderSessions) {
            if (!sessions.has(sessionId)) {
                console.log(`[${this.nodeId}] removing extra mountpoint ${sessionId} `);

                try {
                    await this.forwarder.sendCommand(`REMOVE ${sessionId} `);
                } catch (err) {
                    console.error(`[${this.nodeId}] Failed to remove ${sessionId}: `, err.message);
                }
            }
        }
    }

    // MOUNTPOINT MANAGEMENT
    async onSessionCreated(event) {
        const { sessionId, treeId } = event;

        // Verifica tree
        if (treeId !== this.treeId) {
            console.warn(`[${this.nodeId}] Session event for wrong tree: ${treeId}`);
            return;
        }

        // console.log(`[${this.nodeId}] Received session-created event for ${sessionId}`);

        try {
            await this.createMountpoint(sessionId);
        } catch (error) {
            console.error(`[${this.nodeId}] Failed to create mountpoint`, error.message);
        }
    }

    async onSessionDestroyed(event) {
        const { sessionId, treeId } = event;

        // Verifica tree
        if (treeId !== this.treeId) {
            return;
        }

        // console.log(`[${this.nodeId}] Received session-destroyed event for ${sessionId}`);

        try {
            await this.destroyMountpoint(sessionId);
        } catch (error) {
            console.error(`[${this.nodeId}] Failed to destroy mountpoint`, error.message);
        }
    }

    async createMountpoint(sessionId) {
        // check duplicati
        if (this.mountpoints.has(sessionId)) {
            throw new Error(`Mountpoint for session ${sessionId} already exists`);
        }

        // lock
        if (this.operationLocks.get(sessionId)) {
            throw new Error(`Operation already in progress for session ${sessionId}`);
        }
        this.operationLocks.set(sessionId, true);

        try {
            console.log(`[${this.nodeId}] Creating mountpoint for session: ${sessionId}`);

            // Leggi roomId da Redis (mountpointId = roomId)
            const sessionData = await this.redis.hgetall(`session:${sessionId}`);
            if (!sessionData || !sessionData.roomId) {
                throw new Error(`Session ${sessionId} not found in Redis`);
            }

            const mountpointId = parseInt(sessionData.roomId);
            const audioSsrc = parseInt(sessionData.audioSsrc);
            const videoSsrc = parseInt(sessionData.videoSsrc);
            //console.log(`[${this.nodeId}] Mountpoint ID: ${mountpointId}`);

            // Alloca porte dal pool
            const { audioPort, videoPort } = this.portPool.allocate();
            console.log(`[${this.nodeId}] Allocated ports - Audio: ${audioPort}, Video: ${videoPort}`);

            // Crea mountpoint su Janus Streaming
            await createJanusMountpoint(this.janusStreaming, this.nodeId, mountpointId, audioPort, videoPort, this.mountpointSecret);

            // Crea WHEP endpoint
            // console.log(`Session ID : ${sessionId}`)
            const endpoint = this.whepServer.createEndpoint({
                id: sessionId,
                mountpoint: mountpointId,
                token: this.whepToken
            });

            const createdAt = Date.now();

            // Salva in memoria
            this.mountpoints.set(sessionId, {
                sessionId,
                mountpointId,
                audioSsrc,
                videoSsrc,
                janusAudioPort: audioPort,
                janusVideoPort: videoPort,
                endpoint,
                active: true,
                createdAt
            });

            // Salva su Redis
            await saveMountpointToRedis(this.redis, this.treeId, this.nodeId, {
                sessionId,
                mountpointId,
                audioSsrc,
                videoSsrc,
                janusAudioPort: audioPort,
                janusVideoPort: videoPort,
                createdAt
            });

            // console.log(`[${this.nodeId}] Mountpoint created: ${sessionId}`);
            // console.log(`[${this.nodeId}] WHEP endpoint: ${this.whepBasePath}/endpoint/${sessionId}`);


            // INVIA COMANDO ADD AL PROCESSO C
            try {
                // Formato: ADD <sessionId> <audioSsrc> <videoSsrc> <audioPort> <videoPort>
                const command = `ADD ${sessionId} ${audioSsrc} ${videoSsrc} ${audioPort} ${videoPort}`;
                const response = await this.forwarder.sendCommand(command);

                if (response !== 'OK') {
                    throw new Error(`ADD failed: ${response}`);
                }

                console.log(`[${this.nodeId}] ADD command sent to forwarder`);
            } catch (err) {
                console.error(`[${this.nodeId}] Failed to send ADD command:`, err.message);

                // Rollback: rimuovi mountpoint
                this.mountpoints.delete(sessionId);
                this.portPool.release(audioPort, videoPort);
                await destroyJanusMountpoint(this.janusStreaming, this.nodeId, mountpointId);

                throw new Error(`Failed to configure forwarder: ${err.message}`);
            }

            return {
                sessionId,
                mountpointId,
                whepUrl: `${this.whepBasePath}/endpoint/${sessionId}`,
                janusAudioPort: audioPort,
                janusVideoPort: videoPort
            };
        } catch (error) {
            throw error;
        } finally {
            this.operationLocks.delete(sessionId);
        }
    }

    async destroyMountpoint(sessionId) {
        // check esistenza
        if (!this.mountpoints.has(sessionId)) {
            throw new Error(`Mountpoint for session ${sessionId} not found`);
        }
        // lock
        if (this.operationLocks.get(sessionId)) {
            throw new Error(`Operation already in progress for session ${sessionId}`);
        }
        this.operationLocks.set(sessionId, true);

        try {
            console.log(`[${this.nodeId}] Destroying mountpoint for session: ${sessionId}`);

            const mountpoint = this.mountpoints.get(sessionId);
            const { mountpointId, janusAudioPort, janusVideoPort, endpoint } = mountpoint;

            // inattivo
            mountpoint.active = false;

            // INVIA REMOVE AL PROCESSO C
            try {
                // Formato: REMOVE <sessionId>
                const command = `REMOVE ${sessionId}`;
                const response = await this.forwarder.sendCommand(command);

                if (response !== 'OK') {
                    console.warn(`[${this.nodeId}] REMOVE failed: ${response}`);
                }

                console.log(`[${this.nodeId}] REMOVE command sent to forwarder`);
            } catch (err) {
                console.error(`[${this.nodeId}] Failed to send REMOVE command:`, err.message);
            }

            // distruggi endpoint

            if (this.whepServer && endpoint) {
                try {
                    this.whepServer.destroyEndpoint({ id: sessionId });
                    console.log(`[${this.nodeId}] WHEP endpoint ${sessionId} destroyed`);
                } catch (error) {
                    console.error(`[${this.nodeId}] Error destroying WHEP endpoint:`, error.message);
                    // Non bloccare - continua con cleanup
                }
            }


            // Distruggi mountpoint Janus
            try {
                await destroyJanusMountpoint(this.janusStreaming, mountpointId, this.mountpointSecret);
            } catch (err) {
                console.error(`[${this.nodeId}] Error destroying Janus mountpoint:`, err.message);
            }

            // Rilascia porte
            this.portPool.release(janusAudioPort, janusVideoPort);

            // Rimuovi da memoria
            this.mountpoints.delete(sessionId);

            // Deactivate in Redis
            // rimuove dopo 24 ore
            await deactivateMountpointInRedis(this.redis, this.treeId, this.nodeId, sessionId);

            this.operationLocks.delete(sessionId);

            return { sessionId, destroyed: true };

        } catch (error) {
            console.error(`[${this.nodeId}] Error destroying mountpoint ${sessionId}:`, error.message);
            throw error;
        } finally {
            this.operationLocks.delete(sessionId);
        }
    }

    getMountpoint(sessionId) {
        return getMountpointInfo(this.mountpoints, sessionId);
    }

    getAllMountpoints() {
        return getAllMountpointsInfo(this.mountpoints);
    }

    async discoverExistingSessions() {
        if (!this.treeId) return;

        // console.log(`[${this.nodeId}] Discovering existing sessions for tree ${this.treeId}`);

        const sessionIds = await this.redis.smembers(`sessions:${this.treeId}`);

        if (sessionIds.length === 0) {
            console.log(`[${this.nodeId}] No existing sessions found`);
            return;
        }

        // console.log(`[${this.nodeId}] Found ${sessionIds.length} existing sessions`);

        for (const sessionId of sessionIds) {
            const sessionData = await this.redis.hgetall(`session:${sessionId}`);

            if (sessionData.active !== 'true') {
                console.log(`[${this.nodeId}] Skipping inactive session ${sessionId}`);
                continue;
            }

            // CHECK: mountpoint già esisteva per questo nodo?
            const mountpointId = parseInt(sessionData.roomId);
            const existingMountpoint = await this.redis.hgetall(`mountpoint:${this.nodeId}:${sessionId}`);

            try {
                if (existingMountpoint && existingMountpoint.active === 'true') {
                    // RECOVERY
                    console.log(`[${this.nodeId}] Recovering mountpoint for session ${sessionId}`);
                    await this.recoverMountpoint(sessionId, existingMountpoint);
                } else {
                    // mountpoint nuovo
                    console.log(`[${this.nodeId}] Creating new mountpoint for session ${sessionId}`);
                    await this.createMountpoint(sessionId);
                }
            } catch (err) {
                console.error(`[${this.nodeId}] Failed to handle mountpoint for ${sessionId}:`, err.message);
            }
        }

        console.log(`[${this.nodeId}] Discovery complete`);
    }


    async recoverMountpoint(sessionId, mountpointData) {
        if (this.operationLocks.get(sessionId)) {
            throw new Error(`Operation already in progress for session ${sessionId}`);
        }
        this.operationLocks.set(sessionId, true);

        try {
            // console.log(`[${this.nodeId}] Recovering mountpoint for session: ${sessionId}`);

            const mountpointId = parseInt(mountpointData.mountpointId);
            const audioSsrc = parseInt(mountpointData.audioSsrc);
            const videoSsrc = parseInt(mountpointData.videoSsrc);
            const audioPort = parseInt(mountpointData.janusAudioPort);
            const videoPort = parseInt(mountpointData.janusVideoPort);

            // console.log(`[${this.nodeId}] Reusing existing ports: ${audioPort}, ${videoPort}`);

            // mountpoint Janus già esiste
            // porte allocate
            this.portPool.markAsAllocated(audioPort, videoPort);

            try {
                await createJanusMountpoint(
                    this.janusStreaming,
                    this.nodeId,
                    mountpointId,
                    audioPort,
                    videoPort,
                    this.mountpointSecret
                );
                console.log(`[${this.nodeId}] Mountpoint ${mountpointId} created on Janus`);
            } catch (err) {
                // ignora errore
                if (err.message && err.message.includes('already exists')) {
                    console.log(`[${this.nodeId}] Mountpoint ${mountpointId} already exists on Janus`);
                } else {
                    this.portPool.release(audioPort, videoPort);
                    throw err;
                }
            }

            // Ricrea solo WHEP endpoint
            const endpoint = this.whepServer.createEndpoint({
                id: sessionId,
                mountpoint: mountpointId,
                token: this.whepToken
            });

            // sovrascrivi in memoria
            this.mountpoints.set(sessionId, {
                sessionId,
                mountpointId,
                audioSsrc,
                videoSsrc,
                janusAudioPort: audioPort,
                janusVideoPort: videoPort,
                endpoint,
                active: true,
                createdAt: parseInt(mountpointData.createdAt)
            });

            // ricrea mapping nel forwarder C
            const command = `ADD ${sessionId} ${audioSsrc} ${videoSsrc} ${audioPort} ${videoPort}`;
            const response = await this.forwarder.sendCommand(command);

            if (response !== 'OK') {
                throw new Error(`ADD failed during recovery: ${response}`);
            }

            console.log(`[${this.nodeId}] Mountpoint recovered successfully`);
        } finally {
            this.operationLocks.delete(sessionId);
        }
    }

    // API ENDPOINTS
    setupEgressAPI() {
        // POST /mountpoint/create
        //this.app.post('/mountpoint/create', async (req, res) => {
        //    try {
        //        const { sessionId, audioSsrc, videoSsrc } = req.body;
        //
        //        if (!sessionId || !audioSsrc || !videoSsrc) {
        //            return res.status(400).json({
        //                error: 'Missing fields: sessionId, audioSsrc, videoSsrc'
        //            });
        //        }
        //
        //        const result = await this.createMountpoint(sessionId, parseInt(audioSsrc), parseInt(videoSsrc));
        //        res.status(201).json(result);
        //    } catch (error) {
        //        console.error(`[${this.nodeId}] Create mountpoint error:`, error.message);
        //        res.status(500).json({ error: error.message });
        //    }
        //});

        // POST /mountpoint/:sessionId/destroy
        //this.app.post('/mountpoint/:sessionId/destroy', async (req, res) => {
        //    try {
        //        const { sessionId } = req.params;
        //        const result = await this.destroyMountpoint(sessionId);
        //        res.json({ message: 'Mountpoint destroyed', ...result });
        //    } catch (error) {
        //        console.error(`[${this.nodeId}] Destroy mountpoint error:`, error.message);
        //        res.status(500).json({ error: error.message });
        //    }
        //});

        // GET /mountpoint/:sessionId
        this.app.get('/mountpoint/:sessionId', (req, res) => {
            try {
                const { sessionId } = req.params;
                const mountpoint = this.getMountpoint(sessionId);

                if (!mountpoint) {
                    return res.status(404).json({ error: 'Mountpoint not found' });
                }

                res.json(mountpoint);
            } catch (error) {
                console.error(`[${this.nodeId}] Get mountpoint error:`, error.message);
                res.status(500).json({ error: error.message });
            }
        });

        // GET /mountpoints
        this.app.get('/mountpoints', (req, res) => {
            try {
                const mountpoints = this.getAllMountpoints();
                res.json({
                    count: mountpoints.length,
                    mountpoints
                });
            } catch (error) {
                console.error(`[${this.nodeId}] Get mountpoints error:`, error.message);
                res.status(500).json({ error: error.message });
            }
        });

        // GET /portpool
        this.app.get('/portpool', (req, res) => {
            try {
                res.json(this.portPool.getStats());
            } catch (error) {
                console.error(`[${this.nodeId}] Get portpool error:`, error.message);
                res.status(500).json({ error: error.message });
            }
        });
        // GET /forwarder
        this.app.get('/forwarder', async (req, res) => {
            try {
                const activeCount = Array.from(this.mountpoints.values())
                    .filter(item => item.active).length;

                // Lista mapping dal processo C
                let mappings = null;
                if (this.forwarder.isReady()) {
                    try {
                        mappings = await this.forwarder.listMountpoints();
                    } catch (err) {
                        console.error(`[${this.nodeId}] Failed to list mappings:`, err.message);
                    }
                }

                const forwarderStatus = this.forwarder.getStatus();
                res.json({
                    running: forwarderStatus.running,
                    processAlive: forwarderStatus.processAlive,
                    socketConnected: forwarderStatus.socketConnected,
                    mountpointsActive: activeCount,
                    mappings
                });
            } catch (error) {
                console.error(`[${this.nodeId}] Get forwarder error:`, error.message);
                res.status(500).json({ error: error.message });
            }
        });

    }

    async getStatus() {
        const baseStatus = await super.getStatus();
        const activeCount = Array.from(this.mountpoints.values())
            .filter(item => item.active).length;
        // Lista destinazioni (se forwarder ready)
        let mappings = null;
        try {
            if (this.forwarder.isReady()) {
                mappings = await this.forwarder.listMountpoints();
            }
        } catch (err) {
            console.error(`[${this.nodeId}] Failed to get mappings:`, err.message);
        }

        const forwarderStatus = this.forwarder.getStatus();
        return {
            ...baseStatus,
            janus: {
                connected: this.janusStreaming !== null,
                url: this.janusUrl,
                rtpDestination: this.rtpDestinationHost
            },
            whep: {
                running: this.whepServer !== null,
                basePath: this.whepBasePath
            },
            forwarder: {
                running: forwarderStatus.running,
                processAlive: forwarderStatus.processAlive,
                socketConnected: forwarderStatus.socketConnected,
                mappings
            },
            mountpoints: {
                active: activeCount,
                list: this.getAllMountpoints()
            },
            portPool: this.portPool.getStats()
        };
    }
}