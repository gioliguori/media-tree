import { JanusWhipServer } from 'janus-whip-server';

export class InjectionNode extends BaseNode {
    constructor(config) {
        super(config.nodeId, 'injection', config);

        // WHIP Server
        this.whipServer = null;
        this.janusUrl = config.janus.videoroom.wsUrl;
        this.whipBasePath = config.whip?.basePath || '/whip';
        this.whipToken = config.whip?.token || 'verysecret';
        this.whipSecret = config.whip?.secret || 'adminpwd';

        // Sessione unica
        this.currentSession = {
            sessionId: null,
            roomId: null,
            recipient: null,
            createdAt: null,
            active: false
        };
    }

    // ============ LIFECYCLE ============

    async onInitialize() {
        console.log(`[${this.nodeId}] Initializing WHIP server...`);

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
    }

    async onStop() {
        console.log(`[${this.nodeId}] Stopping injection node...`);

        // Distruggi sessione se attiva
        if (this.currentSession.active) {
            try {
                await this.destroySession();
            } catch (error) {
                console.error(`[${this.nodeId}] Error destroying session on stop:`, error.message);
            }
        }

        // Distruggi WHIP server
        if (this.whipServer) {
            try {
                await this.whipServer.destroy();
                console.log(`[${this.nodeId}] WHIP server destroyed`);
            } catch (error) {
                console.error(`[${this.nodeId}] Error destroying WHIP server:`, error.message);
            }
        }

        console.log(`[${this.nodeId}] Injection node stopped`);
    }

    // ============ TOPOLOGY ============

    async onChildrenChanged(added, removed) {

        if (added.length > 0) {
            console.log(`[${this.nodeId}] Added children:`, added);
        }
        if (removed.length > 0) {
            console.log(`[${this.nodeId}] Removed children:`, removed);
        }

        // Se non c'è sessione attiva, skip
        if (!this.currentSession.active) {
            console.log(`[${this.nodeId}] No active session, ignoring topology change`);
            return;
        }

        // Recupera info nuovo child (prende il primo disponibile)
        const newChildId = added[0] || this.children[0];

        if (!newChildId) {
            console.warn(`[${this.nodeId}] ⚠️ No child available after topology change!`);
            return;
        }

        const childInfo = await this.getNodeInfo(newChildId);
        if (!childInfo) {
            console.error(`[${this.nodeId}] Child ${newChildId} not found in Redis`);
            return;
        }

        const newRecipient = {
            host: childInfo.host,
            audioPort: parseInt(childInfo.audioPort),
            videoPort: parseInt(childInfo.videoPort)
        };

        console.log(`[${this.nodeId}] ⚠️ Child changed to ${newChildId} (${newRecipient.host})`);
        console.log(`[${this.nodeId}] Recreating endpoint with new recipient...`);

        // STRATEGIA MVP: Distruggi e ricrea endpoint
        // Il broadcaster dovrà riconnettersi (downtime ~2-5 secondi)
        const { sessionId, roomId } = this.currentSession;

        try {
            await this.destroySession();
            await new Promise(resolve => setTimeout(resolve, 500));
            await this.createSession(sessionId, roomId, newRecipient);
            console.log(`[${this.nodeId}] Endpoint recreated, now forwarding to ${newRecipient.host}`);
        } catch (error) {
            console.error(`[${this.nodeId}] Failed to recreate endpoint:`, error.message);
        }
    }

    // ==

    // ============ SESSION MANAGEMENT ============

    async createSession(sessionId, roomId, recipient) {
        console.log(`[${this.nodeId}] Creating session ${sessionId}...`);

        // Validazione input
        if (!sessionId || !roomId || !recipient) {
            throw new Error('Missing required fields: sessionId, roomId, recipient');
        }

        if (!recipient.host || !recipient.audioPort || !recipient.videoPort) {
            throw new Error('Invalid recipient: must have host, audioPort, videoPort');
        }

        // Check sessione già attiva
        if (this.currentSession.active) {
            throw new Error(`Session already active: ${this.currentSession.sessionId}`);
        }

        // Crea endpoint WHIP
        try {
            this.whipServer.createEndpoint({
                id: String(sessionId),
                room: roomId,
                token: this.whipToken,
                secret: this.whipSecret,
                recipient: {
                    host: recipient.host,
                    audioPort: recipient.audioPort,
                    videoPort: recipient.videoPort
                }
            });
        } catch (error) {
            console.error(`[${this.nodeId}] Failed to create WHIP endpoint:`, error.message);
            throw error;
        }

        // Salva stato sessione
        this.currentSession = {
            sessionId,
            roomId,
            recipient,
            active: true,
            createdAt: Date.now()
        };

        console.log(`[${this.nodeId}]    Session ${sessionId} created`);
        console.log(`[${this.nodeId}]    Room: ${roomId}`);
        console.log(`[${this.nodeId}]    Forwarding RTP to: ${recipient.host}:${recipient.audioPort}/${recipient.videoPort}`);
        console.log(`[${this.nodeId}]    WHIP endpoint: ${this.whipBasePath}/endpoint/${sessionId}`);

        return {
            sessionId,
            endpoint: `${this.whipBasePath}/endpoint/${sessionId}`,
            roomId,
            recipient
        };
    }



    async destroySession() {
        console.log(`[${this.nodeId}] Destroying session...`);

        // Check sessione attiva
        if (!this.currentSession.active) {
            throw new Error('No active session to destroy');
        }

        const sessionId = this.currentSession.sessionId;

        try {
            // Distruggi endpoint WHIP
            this.whipServer.destroyEndpoint(String(sessionId));

            console.log(`[${this.nodeId}] WHIP endpoint ${sessionId} destroyed`);
        } catch (error) {
            console.error(`[${this.nodeId}] Error destroying WHIP endpoint:`, error.message);
            throw error;
        }

        // Reset stato
        this.currentSession = {
            sessionId: null,
            roomId: null,
            recipient: null,
            active: false,
            createdAt: null
        };

        console.log(`[${this.nodeId}] Session ${sessionId} destroyed`);

        return { sessionId };
    }


    // ============ API ENDPOINTS ============

    setupInjectionAPI() {
        // POST /session/create
        this.app.post('/session/create', async (req, res) => {
            try {
                const { sessionId, roomId, recipient } = req.body;

                const result = await this.createSession(sessionId, roomId, recipient);

                res.status(201).json(result);
            } catch (error) {
                console.error(`[${this.nodeId}] Create session error:`, error.message);
            }
        });

        // POST /session/destroy
        // non chiede id perchè stiamo ragionando su unica sessione attualmente
        this.app.post('/session/destroy', async (req, res) => {
            try {
                const result = await this.destroySession();

                res.json({ message: 'Session destroyed', ...result });
            } catch (error) {
                console.error(`[${this.nodeId}] Destroy session error:`, error.message);
            }
        });

        // GET /session (info sessione corrente)
        this.app.get('/session', (req, res) => {
            if (this.currentSession.active) {
                res.json({
                    active: true,
                    sessionId: this.currentSession.sessionId,
                    roomId: this.currentSession.roomId,
                    recipient: this.currentSession.recipient,
                    createdAt: this.currentSession.createdAt,
                    uptime: Math.floor((Date.now() - this.currentSession.createdAt) / 1000)
                });
            } else {
                res.json({
                    active: false,
                    sessionId: null
                });
            }
        });
    }


    async getStatus() {
        const baseStatus = await super.getStatus();

        return {
            ...baseStatus,
            janus: {
                connected: this.whipServer !== null,
                url: this.janusUrl
            },
            whip: {
                running: this.whipServer !== null,
                basePath: this.whipBasePath
            },
            session: this.currentSession.active ? {
                active: true,
                sessionId: this.currentSession.sessionId,
                roomId: this.currentSession.roomId,
                recipient: this.currentSession.recipient,
                createdAt: this.currentSession.createdAt,
                uptime: Math.floor((Date.now() - this.currentSession.createdAt) / 1000)
            } : {
                active: false,
                sessionId: null
            }
        };
    }
}