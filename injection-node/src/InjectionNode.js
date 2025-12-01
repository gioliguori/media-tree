import { BaseNode } from '../shared/BaseNode.js';
import { connectToJanusVideoroom, createJanusRoom, destroyJanusRoom } from './janus-videoroom-utils.js';
import { saveSessionToRedis, deactivateSessionInRedis, getSessionInfo, getAllSessionsInfo } from './session-utils.js';
import { JanusWhipServer } from 'janus-whip-server'


export class InjectionNode extends BaseNode {
    constructor(config) {
        super(config.nodeId, 'injection', config);

        // WHIP Server
        this.whipServer = null;
        this.janusUrl = config.janus.videoroom.wsUrl;
        this.janusApiSecret = config.janus.videoroom.apiSecret || null;
        this.whipBasePath = config.whip?.basePath || '/whip';
        this.whipToken = config.whip?.token || 'verysecret';
        this.roomSecret = config.janus.videoroom.roomSecret || 'adminpwd';


        // Janus connection pool
        this.janusConnection = null;
        this.janusSession = null;
        this.janusVideoRoom = null;

        // sessione
        this.sessions = new Map(); // sessionId -> sessionData
        // sessionData = {
        //   roomId: 1234,
        //   audioSsrc: 1111,
        //   videoSsrc: 2222,
        //   recipients: [{ host, audioPort, videoPort }],
        //   endpoint: whipEndpoint,
        //   active: true,
        //   createdAt: timestamp
        // }

        // lock
        this.operationLocks = new Map();
    }

    async onInitialize() {
        console.log(`[${this.nodeId}] Initializing WHIP server...`);

        // connessione a janus per gestione room
        const { connection, session, videoRoom } = await connectToJanusVideoroom(this.nodeId, {
            wsUrl: this.janusUrl,
            apiSecret: this.janusApiSecret
        });

        this.janusConnection = connection;
        this.janusSession = session;
        this.janusVideoRoom = videoRoom;

        // Inizializza JanusWhipServer
        this.whipServer = new JanusWhipServer({
            janus: { address: this.janusUrl },
            rest: { app: this.app, basePath: this.whipBasePath }
        });
        console.log(`[${this.nodeId}] WHIP server initialized (Janus: ${this.janusUrl})`);
    }

    async onStart() {
        console.log(`[${this.nodeId}] Starting WHIP server...`);
        await this.whipServer.start();
        console.log(`[${this.nodeId}] WHIP server listening on ${this.whipBasePath}`);

        await this.discoverExistingSessions();

        console.log(`[${this.nodeId}] Injection node ready`);
    }

    async onStop() {
        console.log(`[${this.nodeId}] Stopping injection node...`);

        // Distruggi tutte le sessioni attive
        const sessionIds = Array.from(this.sessions.keys());
        console.log(`[${this.nodeId}] Destroying ${sessionIds.length} active sessions...`);

        for (const sessionId of sessionIds) {
            try {
                await this.destroySession(sessionId);
            } catch (error) {
                console.error(`[${this.nodeId}] Error destroying session ${sessionId}:`, error.message);
            }
        }

        // Stop WHIP server
        if (this.whipServer) {
            try {
                await this.whipServer.stop();
                console.log(`[${this.nodeId}] WHIP server stopped`);
            } catch (error) {
                console.error(`[${this.nodeId}] Error stopping WHIP server:`, error.message);
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

        console.log(`[${this.nodeId}] Injection node stopped`);
    }

    // SESSION MANAGEMENT

    async createSession(sessionId, roomId, audioSsrc, videoSsrc, recipients) {

        // Check duplicati
        if (this.sessions.has(sessionId)) {
            throw new Error(`Session ${sessionId} already exists`);
        }
        // lock
        if (this.operationLocks.get(sessionId)) {
            throw new Error(`Operation already in progress for session ${sessionId}`);
        }
        this.operationLocks.set(sessionId, true);

        try {
            console.log(`[${this.nodeId}] Creating session: ${sessionId}`);
            console.log(`[${this.nodeId}] Room ID: ${roomId}`);
            console.log(`[${this.nodeId}] Audio SSRC: ${audioSsrc}`);
            console.log(`[${this.nodeId}] Video SSRC: ${videoSsrc}`);
            console.log(`[${this.nodeId}] Recipients: ${JSON.stringify(recipients)}`);


            // crea room su janus videoroom
            await createJanusRoom(this.janusVideoRoom, roomId, `Session ${sessionId}`, this.roomSecret);

            if (!audioSsrc || !videoSsrc) {
                throw new Error('Missing SSRC values');
            }
            // crea whip endpoint
            const endpoint = this.whipServer.createEndpoint({
                id: sessionId,
                token: this.whipToken,
                customize: (settings) => {
                    settings.room = roomId;
                    settings.secret = this.roomSecret;
                    settings.label = `Publisher ${sessionId}`;

                    settings.recipients = recipients.map(item => ({
                        host: item.host,
                        audioPort: item.audioPort,
                        audioSsrc: audioSsrc,
                        videoPort: item.videoPort,
                        videoSsrc: videoSsrc,
                        videoRtcpPort: item.videoPort + 1
                    }));
                }
            });

            const createdAt = Date.now();
            // salva in memoria
            this.sessions.set(sessionId, {
                sessionId,
                roomId,
                audioSsrc,
                videoSsrc,
                recipients,
                endpoint,
                active: true,
                createdAt
            });

            // salva su Redis
            await saveSessionToRedis(this.redis, this.treeId, this.nodeId, {
                sessionId,
                roomId,
                audioSsrc,
                videoSsrc,
                recipients,
                createdAt
            });

            console.log(`[${this.nodeId}] Session created: ${sessionId}`);
            console.log(`[${this.nodeId}] WHIP endpoint: ${this.whipBasePath}/endpoint/${sessionId}`);
            return {
                sessionId,
                endpoint: `${this.whipBasePath}/endpoint/${sessionId}`,
                roomId,
                audioSsrc,
                videoSsrc,
                recipients
            };
        } finally {
            // Unlock
            this.operationLocks.delete(sessionId);
        }
    }

    async destroySession(sessionId) {
        // Check esistenza
        if (!this.sessions.has(sessionId)) {
            throw new Error(`Session ${sessionId} not found`);
        }
        // Lock
        if (this.operationLocks.get(sessionId)) {
            throw new Error(`Operation already in progress for session ${sessionId}`);
        }
        this.operationLocks.set(sessionId, true);

        try {
            console.log(`[${this.nodeId}] Destroying session: ${sessionId}`);

            const session = this.sessions.get(sessionId);
            const { roomId, endpoint } = session;

            // inattiva su Redis
            await deactivateSessionInRedis(this.redis, this.treeId, this.nodeId, sessionId);

            // distruggi endpoint
            try {
                if (this.whipServer && endpoint) {
                    this.whipServer.destroyEndpoint({ id: sessionId });
                    console.log(`[${this.nodeId}] WHIP endpoint ${sessionId} destroyed`);
                }
            } catch (error) {
                console.error(`[${this.nodeId}] Error destroying WHIP endpoint:`, error.message);
                // Non bloccare se endpoint già distrutto
            }

            // distruggi room
            await destroyJanusRoom(this.janusVideoRoom, this.nodeId, roomId, this.roomSecret);

            // rimuovi da memoria
            this.sessions.delete(sessionId);

            console.log(`[${this.nodeId}] Session destroyed: ${sessionId}`);

            return { sessionId };

        } catch (error) {
            console.error(`[${this.nodeId}] Error destroying session ${sessionId}:`, error.message);
            throw error;
        } finally {
            // Unlock
            this.operationLocks.delete(sessionId);
        }
    }


    getSession(sessionId) {
        return getSessionInfo(this.sessions, sessionId);
    }

    getAllSessions() {
        return getAllSessionsInfo(this.sessions);
    }

    async discoverExistingSessions() {
        console.log(`[${this.nodeId}] Discovering existing sessions from Redis...`);

        try {
            const sessionIds = await this.redis.smembers(`tree:${this.treeId}:sessions:node:${this.nodeId}`);

            if (sessionIds.length === 0) {
                console.log(`[${this.nodeId}] No existing sessions found`);
                return;
            }

            console.log(`[${this.nodeId}] Found ${sessionIds.length} sessions in Redis`);

            for (const sessionId of sessionIds) {
                // legge da Redis
                const sessionData = await this.redis.hgetall(`tree:${this.treeId}:session:${sessionId}`);

                if (!sessionData || Object.keys(sessionData).length === 0) {
                    console.warn(`[${this.nodeId}] Session ${sessionId} not found in Redis`);
                    continue;
                }

                try {
                    await this.recoverSession(sessionId, sessionData);
                } catch (err) {
                    console.error(`[${this.nodeId}] Failed to recover session ${sessionId}:`, err.message);
                }
            }

            console.log(`[${this.nodeId}] Session recovery completed`);
        } catch (error) {
            console.error(`[${this.nodeId}] Error during session discovery:`, error.message);
            throw error;
        }
    }

    async recoverSession(sessionId, sessionData) {
        if (this.operationLocks.get(sessionId)) {
            throw new Error(`Operation already in progress for session ${sessionId}`);
        }
        this.operationLocks.set(sessionId, true);

        try {
            console.log(`[${this.nodeId}] Recovering session: ${sessionId}`);
            const roomId = parseInt(sessionData.roomId);
            const audioSsrc = parseInt(sessionData.audioSsrc);
            const videoSsrc = parseInt(sessionData.videoSsrc);

            // Recipients da topologia corrente (recuperata in BaseNode.updateTopology())
            const recipients = await this.getRecipientsFromTopology();

            console.log(`[${this.nodeId}] Session data: room=${roomId}, audio=${audioSsrc}, video=${videoSsrc}`);

            // Try-create Janus room (potrebbe già esistere)
            try {
                await createJanusRoom(
                    this.janusVideoRoom,
                    roomId,
                    `Session ${sessionId}`,
                    this.roomSecret
                );
                console.log(`[${this.nodeId}] Room ${roomId} created on Janus`);
            } catch (err) {
                if (err.message && err.message.includes('already exists')) {
                    console.log(`[${this.nodeId}] Room ${roomId} already exists on Janus, reusing it`);
                } else {
                    throw err;
                }
            }

            // Ricrea WHIP endpoint
            const endpoint = this.whipServer.createEndpoint({
                id: sessionId,
                token: this.whipToken,
                customize: (settings) => {
                    settings.room = roomId;
                    settings.secret = this.roomSecret;
                    settings.label = `Publisher ${sessionId}`;

                    settings.recipients = recipients.map(item => ({
                        host: item.host,
                        audioPort: item.audioPort,
                        audioSsrc: audioSsrc,
                        videoPort: item.videoPort,
                        videoSsrc: videoSsrc,
                        videoRtcpPort: item.videoPort + 1
                    }));
                }
            });

            // salva memoria
            this.sessions.set(sessionId, {
                sessionId,
                roomId,
                audioSsrc,
                videoSsrc,
                recipients,
                endpoint,
                active: true,
                createdAt: parseInt(sessionData.createdAt)
            });

            console.log(`[${this.nodeId}] Session ${sessionId} recovered successfully`);
        } finally {
            this.operationLocks.delete(sessionId);
        }
    }
    // TOPOLOGY

    async onChildrenChanged(added, removed) {
        // recipients statici
    }

    // API ENDPOINTS

    setupInjectionAPI() {
        // POST /session/create
        this.app.post('/session', async (req, res) => {
            try {
                const { sessionId, roomId, audioSsrc, videoSsrc } = req.body;

                if (!sessionId || !roomId || !audioSsrc || !videoSsrc) {
                    return res.status(400).json({
                        error: 'Missing fields: sessionId, roomId, audioSsrc, videoSsrc'
                    });
                }

                // prendiamo direttamente da redis (non inviamo pià nella POST)
                const recipients = await this.getRecipientsFromTopology();

                const result = await this.createSession(sessionId, roomId, audioSsrc, videoSsrc, recipients);

                // Risponde al Controller
                res.status(201).json({
                    success: true,
                    sessionId: result.sessionId,
                    treeId: this.treeId,
                    roomId: result.roomId,
                    audioSsrc: result.audioSsrc,
                    videoSsrc: result.videoSsrc,
                    endpoint: result.endpoint
                });


            } catch (error) {
                console.error(`[${this.nodeId}] Create session error:`, error.message);
                res.status(500).json({
                    success: false,
                    error: error.message
                });
            }
        });

        // POST /session/:sessionId/destroy
        this.app.post('/session/:sessionId/destroy', async (req, res) => {
            try {
                const { sessionId } = req.params;
                const result = await this.destroySession(sessionId);
                res.json({ message: 'Session destroyed', ...result });
            } catch (error) {
                console.error(`[${this.nodeId}] Destroy session error:`, error.message);
                res.status(500).json({ error: error.message });
            }
        });

        // GET /session/:sessionId
        this.app.get('/session/:sessionId', (req, res) => {
            try {
                const { sessionId } = req.params;
                const session = this.getSession(sessionId);

                if (!session) {
                    return res.status(404).json({ error: 'Session not found' });
                }

                res.json(session);
            } catch (error) {
                console.error(`[${this.nodeId}] Get session error:`, error.message);
                res.status(500).json({ error: error.message });
            }
        });

        // GET /sessions
        this.app.get('/sessions', (req, res) => {
            try {
                const sessions = this.getAllSessions();
                res.json({
                    count: sessions.length,
                    sessions
                });
            } catch (error) {
                console.error(`[${this.nodeId}] Get sessions error:`, error.message);
                res.status(500).json({ error: error.message });
            }
        });
    }

    async getRecipientsFromTopology() {
        const recipients = [];

        for (const childId of this.children) {
            const childInfo = await this.getNodeInfo(childId);
            if (childInfo) {
                recipients.push({
                    host: childInfo.host,
                    audioPort: parseInt(childInfo.audioPort),
                    videoPort: parseInt(childInfo.videoPort)
                });
            }
        }

        return recipients;
    }

    async getStatus() {
        const baseStatus = await super.getStatus();
        const activeCount = Array.from(this.sessions.values())
            .filter(item => item.active).length;
        return {
            ...baseStatus,
            janus: {
                connected: this.janusVideoRoom !== null,
                url: this.janusUrl
            },
            whip: {
                running: this.whipServer !== null,
                basePath: this.whipBasePath
            },
            sessions: {
                active: activeCount,
                list: this.getAllSessions()
            }
        };
    }
}