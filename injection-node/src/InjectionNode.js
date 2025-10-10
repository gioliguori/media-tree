import { BaseNode } from '../shared/BaseNode.js';
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

        // lock
        this.isRecreatingEndpoint = false;
        this.isDestroyingSession = false;
    }

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

        console.log(`isRecreatingEndpoint: ${this.isRecreatingEndpoint}`);
        console.log(`Session active: ${this.currentSession.active}`);
        console.log(`Session ID: ${this.currentSession.sessionId}`);

        // se non c'è sessione attiva, skip
        if (!this.currentSession.active) {
            console.log(`[${this.nodeId}] No active session`);
            return;
        }

        // info nuovo child (prende il primo disponibile)
        const newChildId = added[0] || this.children[0];

        if (!newChildId) {
            console.warn(`[${this.nodeId}] No child available`);
            return;
        }

        // se il child è lo stesso di prima, skip
        if (this.currentSession.recipient &&
            this.currentSession.recipient.host === newChildId) {
            console.log(`[${this.nodeId}] Child unchanged`);
            return;
        }

        // semaforo
        if (this.isRecreatingEndpoint) {
            console.log(`[${this.nodeId}] Already recreating`);
            return;
        }

        this.isRecreatingEndpoint = true;

        try {
            const childInfo = await this.getNodeInfo(newChildId);

            if (!childInfo) {
                console.error(`[${this.nodeId}] Child ${newChildId} not found`);
                return;
            }

            const newRecipient = {
                host: childInfo.host,
                audioPort: parseInt(childInfo.audioPort),
                videoPort: parseInt(childInfo.videoPort)
            };

            console.log(`[${this.nodeId}] Child changed`);
            console.log(`[${this.nodeId}] Recreating endpoint`);

            // Salva dati correnti
            const { sessionId, roomId } = this.currentSession;
            console.log(`[${this.nodeId}] Old session: ${sessionId}`);

            // Distruggi sessione corrente
            await this.destroySession();
            console.log(`[${this.nodeId}] Session destroyed`);
            await new Promise(resolve => setTimeout(resolve, 500));
            // attualmente non funziona :
            // - se distruggiamo endpoint in destroySession() mi da errore sulla distruzione, dice che già è stato distrutto, credo 
            //   dalla libreria JanusWhipServer, se non lo distruggo mi dice endpoint gia esistente. 
            //   la soluzione commentata funziona (crea endpoint con nuovo sessionId).
            //   comunque anche se funzionasse è la soluzione sbagliata
            //   servierebbe updateForwarder()

            await this.createSession(sessionId, roomId, newRecipient);
            console.log(`[${this.nodeId}] Endpoint recreated`);

            //// Crea nuova sessione
            //const newSessionId = `${sessionId}-${Date.now()}`;
            //console.log(`[${this.nodeId}] Creating new session: ${newSessionId}`);
            //
            //await this.createSession(newSessionId, roomId, newRecipient);
            //
            //console.log(`[${this.nodeId}] Endpoint recreated`);
            //console.log(`\n========================================`);
            //console.log(`ENDPOINT RECREATED SUCCESSFULLY!`);
            //console.log(`========================================`);
            //console.log(`Old ID: ${sessionId}`);
            //console.log(`New ID: ${newSessionId}`);
            //console.log(`New recipient: ${newRecipient.host}:${newRecipient.audioPort}/${newRecipient.videoPort}`);
            //console.log(`\n RESTART BROADCASTER WITH NEW URL:`);
            //console.log(`docker-compose -f docker-compose.test.yaml --profile streaming run -d --rm -e URL=http://injection-1:7070/whip/endpoint/${newSessionId} whip-client`);
            //console.log(`========================================\n`);
        } catch (error) {
            console.error(`[${this.nodeId}] Error in onChildrenChange`, error.message);
        } finally {
            console.log(`[${this.nodeId}] Releasing flag`);
            this.isRecreatingEndpoint = false;
        }
    }

    // ============ SESSION MANAGEMENT ============

    async createSession(sessionId, roomId, recipient) {
        console.log(`[${this.nodeId}] Creating session ${sessionId}...`);

        // Validazione input
        if (!sessionId || !roomId || !recipient) {
            throw new Error('Missing fields: sessionId, roomId, recipient');
        }

        if (!recipient.host || !recipient.audioPort || !recipient.videoPort) {
            throw new Error('Invalid recipient');
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
        const destroyId = Date.now();
        console.log(`   Session active: ${this.currentSession.active}`);
        console.log(`   Session ID: ${this.currentSession.sessionId}`);
        console.log(`   isDestroyingSession: ${this.isDestroyingSession}`);

        // Check se già in corso
        if (this.isDestroyingSession) {
            console.warn(`[${this.nodeId}] destroySession already in progress`);
            return { sessionId: this.currentSession.sessionId };
        }

        // Check sessione attiva
        if (!this.currentSession.active) {
            console.error(`[${this.nodeId}] No active session to destroy`);
            throw new Error('No active session to destroy');
        }

        // lock
        this.isDestroyingSession = true;

        const sessionId = this.currentSession.sessionId;
        console.log(`[${this.nodeId}] Destroying session: ${sessionId}`);

        try {
            // Reset PRIMA stato della sessione
            this.currentSession.active = false;
            // Distruggi endpoint WHIP
            // La libreria janus-whip-server fa cleanup automatico?
            // il cleanup automatico della libreria → "Invalid endpoint ID" error
            //??? try{this.whipServer.destroyEndpoint(String(sessionId));
            //console.log(`[${this.nodeId}] WHIP endpoint ${sessionId} destroyed`);
            //} catch (error) {

            //console.error(`[${this.nodeId}] Error destroying WHIP endpoint:`, error.message);
            //throw error;
            //}

        } finally {
            // Reset completo stato
            this.currentSession = {
                sessionId: null,
                roomId: null,
                recipient: null,
                active: false,
                createdAt: null
            };

            // unlock
            this.isDestroyingSession = false;

            console.log(`[${this.nodeId}] Session destroyed`);
        }

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
                res.status(500).json({ error: error.message });
            }
        });

        // POST /session/destroy
        this.app.post('/session/destroy', async (req, res) => {
            try {
                const result = await this.destroySession();
                res.json({ message: 'Session destroyed', ...result });
            } catch (error) {
                console.error(`[${this.nodeId}] Destroy session error:`, error.message);
                res.status(500).json({ error: error.message });
            }
        });

        // GET /session
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