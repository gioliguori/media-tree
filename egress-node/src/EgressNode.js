import { BaseNode } from '../shared/BaseNode.js';
import { PortPool } from '../shared/PortPool.js';
import { saveMountpointToRedis, deactivateMountpointInRedis, getMountpointInfo, getAllMountpointsInfo } from './mountpoint-utils.js';
import { connectToJanusStreaming, createJanusMountpoint, destroyJanusMountpoint } from './janus-streaming-utils.js';

import { JanusWhepServer } from 'janus-whep-server';
import { spawn } from 'child_process';
import express from 'express';

import net from 'net';                      // per socket unix
import path from 'path';
import fs from 'fs/promises';               // filesystem promise per check 

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


        // Percorso socket Unix
        this.socketPath = `/tmp/egress-forwarder-${this.nodeId}.sock`;


        // process.cwd() = directory corrente (in Docker /app)
        this.forwarderPath = path.join(process.cwd(), 'forwarder', 'egress-forwarder');


        // Riferimenti processo C e socket
        this.forwarderProcess = null;
        this.forwarderSocket = null;
        this.isForwarderReady = false;

        // Health check processo C (PING ogni 30s)
        this.healthCheckInterval = null;

        // Map per tracciare comandi in attesa: commandId -> { cmd, resolve, reject, buffer }
        this.pendingCommands = new Map();


        // Counter ID comandi
        this.commandId = 0;

        // Lock per operazioni per sessione
        this.operationLocks = new Map();
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
        await this.startForwarder();

        // PING ogni 30s
        this.startHealthCheck();
        console.log(`[${this.nodeId}] Egress node ready`);
    }

    async onStop() {
        console.log(`[${this.nodeId}] Stopping egress node...`);

        // Ferma health check
        if (this.healthCheckInterval) {
            clearInterval(this.healthCheckInterval);
            this.healthCheckInterval = null;
        }

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

        // Chiudi socket
        if (this.forwarderSocket) {
            try {
                // Prova a inviare SHUTDOWN al forwarder (best effort)
                await this.sendCommand('SHUTDOWN');
            } catch (err) {
                console.error(`[${this.nodeId}] Error sending SHUTDOWN:`, err.message);
            }

            // Chiudi la connessione socket
            this.forwarderSocket.destroy();
            this.forwarderSocket = null;
        }

        // Termina processo C
        if (this.forwarderProcess) {
            this.forwarderProcess.kill('SIGTERM');
            this.forwarderProcess = null;
        }

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

    //  FORWARDER C

    async startForwarder() {
        console.log(`[${this.nodeId}] Starting forwarder...`);
        console.log(`[${this.nodeId}] Socket: ${this.socketPath}`);
        console.log(`[${this.nodeId}] Ports: audio=${this.rtp.audioPort} video=${this.rtp.videoPort}`);

        // Spawn processo C
        // Argomenti: <nodeId> <audioPort> <videoPort> <destinationHost>
        this.forwarderProcess = spawn(this.forwarderPath, [
            this.nodeId,                           // argv[1]
            String(this.rtp.audioPort),            // argv[2]
            String(this.rtp.videoPort),            // argv[3]
            this.rtpDestinationHost                // argv[4]
        ], {
            stdio: ['ignore', 'pipe', 'pipe']
        });

        // Stdout del processo C
        this.forwarderProcess.stdout.on('data', (data) => {
            const msg = data.toString().trim();
            if (msg) {
                console.log(`[${this.nodeId}] [C] ${msg}`);
            }
        });

        // Stderr del processo C
        this.forwarderProcess.stderr.on('data', (data) => {
            const msg = data.toString().trim();
            if (msg) {
                console.error(`[${this.nodeId}] [C ERROR] ${msg}`);
            }
        });

        // Gestione exit processo C
        this.forwarderProcess.on('exit', (code, signal) => {
            console.error(`[${this.nodeId}] Forwarder exited: code=${code} signal=${signal}`);
            this.isForwarderReady = false;

            // TODO: auto-restart se non in shutdown
        });

        // Errore spawn
        this.forwarderProcess.on('error', (err) => {
            console.error(`[${this.nodeId}] Failed to spawn forwarder:`, err.message);
            this.isForwarderReady = false;
        });

        // Aspetta il socket
        await this.waitForSocket();

        // Connetti
        await this.connectToSocket();

        this.isForwarderReady = true;

        // PING di verifica
        const response = await this.sendCommand('PING');
        if (response !== 'PONG') {
            throw new Error(`Unexpected PING response: ${response}`);
        }

        console.log(`[${this.nodeId}] Forwarder ready`);
    }

    // Controlla ogni 100ms se il file socket esiste, max 10 secondi
    async waitForSocket() {
        const maxWait = 10000;       // 10 secondi
        const checkInterval = 100;   // Controlla ogni 100ms
        const startTime = Date.now();

        while (Date.now() - startTime < maxWait) {
            try {
                // fs.access() lancia errore se file non esiste
                await fs.access(this.socketPath);
                return;
            } catch (err) {
                // Socket non ancora pronto, aspetta 100ms
                await new Promise(resolve => setTimeout(resolve, checkInterval));
            }
        }

        throw new Error(`Socket timeout: ${this.socketPath} not created after ${maxWait}ms`);
    }

    // Ritorna Promise che si risolve quando connessione è stabilita
    async connectToSocket() {
        return new Promise((resolve, reject) => {
            // Crea connessione socket Unix
            this.forwarderSocket = net.createConnection(this.socketPath);

            // Connessione stabilita
            this.forwarderSocket.on('connect', () => {
                console.log(`[${this.nodeId}] Socket connected`);
                resolve();
            });

            // Errore connessione
            this.forwarderSocket.on('error', (err) => {
                console.error(`[${this.nodeId}] Socket error:`, err.message);
                reject(err);
            });

            // Socket chiuso (processo C fallito o disconnect)
            this.forwarderSocket.on('close', () => {
                console.log(`[${this.nodeId}] Socket closed`);
                this.isForwarderReady = false;
            });

            // Setup handler per leggere risposte dal processo C
            this.setupSocketDataHandler();

            // Timeout 10 secondi
            setTimeout(() => {
                reject(new Error('Socket connection timeout'));
            }, 10000);
        });
    }

    // Accumula caratteri fino a '\n', poi processa linea completa
    setupSocketDataHandler() {
        // Buffer per accumulare dati parziali
        let buffer = '';

        // Chiamato ogni volta che arrivano dati dal socket
        this.forwarderSocket.on('data', (data) => {
            buffer += data.toString();

            // Cerca righe complete (terminate da '\n')
            let newlineIndex;
            while ((newlineIndex = buffer.indexOf('\n')) !== -1) {
                // Estrai riga completa (senza '\n')
                const line = buffer.substring(0, newlineIndex);

                // Rimuovi riga dal buffer
                buffer = buffer.substring(newlineIndex + 1);

                // Processa la riga (risposta dal processo C)
                this.handleSocketResponse(line);
            }
            // Dati parziali verranno processati al prossimo 'data' event
        });
    }

    handleSocketResponse(line) {
        if (!line) return;

        // Prendi primo comando in attesa
        const firstEntry = this.pendingCommands.entries().next().value;

        if (!firstEntry) {
            console.warn(`[${this.nodeId}] Unexpected response: ${line}`);
            return;
        }

        const [id, pending] = firstEntry;  // [0, { cmd, resolve, reject }]

        // Gestisci risposta

        // Risposte semplici: OK, PONG, BYE
        if (line === 'OK' || line === 'PONG' || line === 'BYE') {
            // Rimuovi dalla lista pending
            this.pendingCommands.delete(id);
            // Risolvi la Promise
            pending.resolve(line);
        }
        // Risposta multiriga
        else if (line === 'END') {
            this.pendingCommands.delete(id);
            // Restituisci buffer accumulato
            pending.resolve(pending.buffer ? pending.buffer.join('\n') : '');
        }
        // Risposta errore
        else if (line.startsWith('ERROR')) {
            this.pendingCommands.delete(id);
            pending.reject(new Error(line));
        }
        // Accumula righe in buffer (per risposte multilinea)
        else {
            if (!pending.buffer) pending.buffer = [];
            pending.buffer.push(line);
        }
    }

    // @param {string} cmd - Comando (es: "PING", "ADD ...")
    async sendCommand(cmd) {
        if (!this.isForwarderReady) {
            throw new Error('Forwarder not ready');
        }

        // Crea Promise che verrà risolta quando arriva risposta
        return new Promise((resolve, reject) => {
            const id = this.commandId++;

            // Timeout: se risposta non arriva entro 5s, rigetta
            const timeout = setTimeout(() => {
                this.pendingCommands.delete(id);
                reject(new Error(`Command timeout: ${cmd}`));
            }, 5000);

            // Quando arriva risposta, handleSocketResponse() chiama resolve/reject
            this.pendingCommands.set(id, {
                cmd,
                resolve: (value) => {
                    clearTimeout(timeout);  // Cancella timeout
                    resolve(value);         // Risolvi Promise
                },
                reject: (err) => {
                    clearTimeout(timeout);
                    reject(err);
                }
            });

            // Invia il comando
            this.forwarderSocket.write(`${cmd}\n`);
        });
    }

    // Health check: PING ogni 30s
    startHealthCheck() {
        console.log(`[${this.nodeId}] Starting health check`);

        this.healthCheckInterval = setInterval(async () => {
            try {
                // Invia PING
                const response = await this.sendCommand('PING');

                // Verifica risposta
                if (response !== 'PONG') {
                    console.warn(`[${this.nodeId}] Unexpected PING response: ${response}`);
                }

            } catch (err) {
                // PING fallito
                console.error(`[${this.nodeId}] Health check failed:`, err.message);

                // Setta flag a false
                this.isForwarderReady = false;

                // TODO: auto-restart
            }
        }, 30000);  // 30 secondi
    }

    // MOUNTPOINT MANAGEMENT

    async createMountpoint(sessionId, audioSsrc, videoSsrc) {
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
            console.log(`[${this.nodeId}] Audio SSRC: ${audioSsrc}`);
            console.log(`[${this.nodeId}] Video SSRC: ${videoSsrc}`);

            // Leggi roomId da Redis (mountpointId = roomId)
            const sessionData = await this.redis.hgetall(`session:${sessionId}`);
            if (!sessionData || !sessionData.roomId) {
                throw new Error(`Session ${sessionId} not found in Redis`);
            }

            const mountpointId = parseInt(sessionData.roomId);
            console.log(`[${this.nodeId}] Mountpoint ID: ${mountpointId} (from roomId)`);

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
            await saveMountpointToRedis(this.redis, this.nodeId, {
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
                const response = await this.sendCommand(command);

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
                const response = await this.sendCommand(command);

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
                await destroyJanusMountpoint(this.janusStreaming, mountpoint.mountpointId, this.mountpointSecret);
            } catch (err) {
                console.error(`[${this.nodeId}] Error destroying Janus mountpoint:`, err.message);
            }

            // Rilascia porte
            this.portPool.release(mountpoint.janusAudioPort, mountpoint.janusVideoPort);

            // Rimuovi da memoria
            this.mountpoints.delete(sessionId);

            // Deactivate in Redis
            // rimuove dopo 24 ore
            await deactivateMountpointInRedis(this.redis, this.nodeId, sessionId);

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


    async listMountpoints() {
        // Chiede al forwarder C la lista dei mapping
        // Risposta formato:
        // audio:sessionId:ssrc:port
        // video:sessionId:ssrc:port
        // END
        try {
            const response = await this.sendCommand('LIST');
            return response;
        } catch (err) {
            console.error(`[${this.nodeId}] Failed to list mountpoints:`, err.message);
            return null;
        }
    }

    // API ENDPOINTS

    setupEgressAPI() {
        // POST /mountpoint/create
        this.app.post('/mountpoint/create', async (req, res) => {
            try {
                const { sessionId, audioSsrc, videoSsrc } = req.body;

                if (!sessionId || !audioSsrc || !videoSsrc) {
                    return res.status(400).json({
                        error: 'Missing fields: sessionId, audioSsrc, videoSsrc'
                    });
                }

                const result = await this.createMountpoint(sessionId, parseInt(audioSsrc), parseInt(videoSsrc));
                res.status(201).json(result);
            } catch (error) {
                console.error(`[${this.nodeId}] Create mountpoint error:`, error.message);
                res.status(500).json({ error: error.message });
            }
        });

        // POST /mountpoint/:sessionId/destroy
        this.app.post('/mountpoint/:sessionId/destroy', async (req, res) => {
            try {
                const { sessionId } = req.params;
                const result = await this.destroyMountpoint(sessionId);
                res.json({ message: 'Mountpoint destroyed', ...result });
            } catch (error) {
                console.error(`[${this.nodeId}] Destroy mountpoint error:`, error.message);
                res.status(500).json({ error: error.message });
            }
        });

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

        // GET /portpool (debug info)
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
                if (this.isForwarderReady) {
                    try {
                        mappings = await this.listMountpoints();
                    } catch (err) {
                        console.error(`[${this.nodeId}] Failed to list mappings:`, err.message);
                    }
                }

                res.json({
                    running: this.isForwarderReady,
                    processAlive: this.forwarderProcess !== null,
                    socketConnected: this.forwarderSocket !== null,
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
            if (this.isForwarderReady) {
                mappings = await this.listMountpoints();
            }
        } catch (err) {
            console.error(`[${this.nodeId}] Failed to get mappings:`, err.message);
        }

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
                running: this.isForwarderReady,
                processAlive: this.forwarderProcess !== null,
                socketConnected: this.forwarderSocket !== null,
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