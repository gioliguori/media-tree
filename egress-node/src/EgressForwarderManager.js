import { spawn } from 'child_process';
import net from 'net';                      // per socket unix
import path from 'path';
import fs from 'fs/promises';               // filesystem promise per check 

export class EgressForwarderManager {
    constructor(config) {
        this.nodeId = config.nodeId;
        this.rtpAudioPort = config.rtpAudioPort;
        this.rtpVideoPort = config.rtpVideoPort;
        this.rtpDestinationHost = config.rtpDestinationHost;

        // Percorso socket Unix
        this.socketPath = `/tmp/egress-forwarder-${this.nodeId}.sock`;

        // process.cwd() = directory corrente (in Docker /app)
        this.forwarderPath = path.join(process.cwd(), 'forwarder', 'egress-forwarder');

        // Riferimenti processo C e socket
        this.forwarderProcess = null;
        this.forwarderSocket = null;
        this.isForwarderReady = false;

        // Health check processo C
        this.healthCheckInterval = null;

        // Map per tracciare comandi in attesa: commandId -> { cmd, resolve, reject, buffer }
        this.pendingCommands = new Map();

        // Counter ID comandi
        this.commandId = 0;

        // Callback per sync
        this.syncCallback = config.syncCallback || null;
    }

    async startForwarder() {
        console.log(`[${this.nodeId}] Starting forwarder...`);
        console.log(`[${this.nodeId}] Socket: ${this.socketPath}`);
        console.log(`[${this.nodeId}] Ports: audio=${this.rtpAudioPort} video=${this.rtpVideoPort}`);

        try {
            await fs.unlink(this.socketPath);
        } catch (err) {
            // Ignora errore (primo avvio o shutdown pulito)
        }
        
        // Spawn processo C
        // Argomenti: <nodeId> <audioPort> <videoPort> <destinationHost>
        this.forwarderProcess = spawn(this.forwarderPath, [
            this.nodeId,                           // argv[1]
            String(this.rtpAudioPort),            // argv[2]
            String(this.rtpVideoPort),            // argv[3]
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

    async killForwarder() {
        console.log(`[${this.nodeId}] Killing forwarder...`);

        // Chiudi socket
        if (this.forwarderSocket) {
            this.forwarderSocket.destroy();
            this.forwarderSocket = null;
        }

        // Kill processo
        if (this.forwarderProcess) {
            this.forwarderProcess.kill('SIGKILL');
            this.forwarderProcess = null;
        }

        // Rimuovi socket
        try {
            await fs.unlink(this.socketPath);
            console.log(`[${this.nodeId}] Socket removed`);
        } catch (err) {
            // File non esiste
        }

        this.isForwarderReady = false;
    }

    // Controlla ogni 100ms
    async waitForSocket() {
        const maxWait = 10000;       // 10 secondi
        const checkInterval = 100;
        const startTime = Date.now();

        while (Date.now() - startTime < maxWait) {
            try {
                // fs.access() lancia errore se file non esiste
                await fs.access(this.socketPath);
                return;
            } catch (err) {
                // aspetta 100ms
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
        // Accumula righe in buffer
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

    async syncForwarder() {
        // Sincronizza stato in memoria con forwarder C
        // console.log(`[${this.nodeId}] Syncing with forwarder...`);

        try {
            // stato attuale dal forwarder
            const forwarderState = await this.listMountpoints();
            if (!forwarderState) {
                console.error(`[${this.nodeId}] Cannot sync: forwarder LIST failed`);
                return;
            }

            const forwarderSessions = new Set();
            forwarderState.audio.forEach(item => forwarderSessions.add(item.sessionId));
            forwarderState.video.forEach(item => forwarderSessions.add(item.sessionId));

            // Usa callback per ottenere mountpoints da EgressNode
            await this.syncCallback(forwarderSessions);

            // console.log(`[${this.nodeId}] Sync completed`);

        } catch (err) {
            console.error(`[${this.nodeId}] Sync error: `, err.message);
        }
    }

    async startPipelines() {
        // Avvia le pipeline GStreamer (PAUSED -> PLAYING)
        if (!this.isForwarderReady) {
            throw new Error('Forwarder not ready');
        }

        const response = await this.sendCommand('PLAY');
        if (response !== 'OK') {
            throw new Error(`PLAY failed: ${response}`);
        }

        console.log(`[${this.nodeId}] Pipelines started (PLAYING)`);
    }
    // Health check
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

                // sincronizzazione
                await this.syncForwarder();

            } catch (err) {
                // PING fallito
                console.error(`[${this.nodeId}] Health check failed:`, err.message);

                // Setta flag a false
                this.isForwarderReady = false;

                // Autorestart forwarder
                try {
                    await this.killForwarder();
                    // wait
                    await new Promise(resolve => setTimeout(resolve, 500));

                    await this.startForwarder();

                    // sincronizzazione
                    await this.syncForwarder();
                    await this.startPipelines();

                    console.log(`[${this.nodeId}] Forwarder recovered successfully`);
                } catch (respawnErr) {
                    console.error(`[${this.nodeId}] Respawn failed:`, respawnErr.message);
                    // ci riprova al prossimo ciclo
                }
            }
        }, 120000);  // 2 minuti
    }

    stopHealthCheck() {
        // Ferma health check
        if (this.healthCheckInterval) {
            clearInterval(this.healthCheckInterval);
            this.healthCheckInterval = null;
        }
    }

    async listMountpoints() {
        // Chiede al forwarder C la lista dei mapping
        // Risposta formato JSON: {"audio": [...], "video": [...]}
        try {
            const response = await this.sendCommand('LIST');
            const parsed = JSON.parse(response);
            return parsed;
        } catch (err) {
            console.error(`[${this.nodeId}] Failed to list mountpoints: `, err.message);
            return null;
        }
    }

    async shutdown() {
        // Chiudi socket
        if (this.forwarderSocket) {
            try {
                // Prova a inviare SHUTDOWN al forwarder
                await this.sendCommand('SHUTDOWN');
            } catch (err) {
                console.error(`[${this.nodeId}] Error sending SHUTDOWN:`, err.message);
            }

            // Chiudi la socket
            this.forwarderSocket.destroy();
            this.forwarderSocket = null;
        }

        // Termina processo C
        if (this.forwarderProcess) {
            this.forwarderProcess.kill('SIGTERM');
            this.forwarderProcess = null;
        }
    }

    isReady() {
        return this.isForwarderReady;
    }

    getStatus() {
        return {
            running: this.isForwarderReady,
            processAlive: this.forwarderProcess !== null,
            socketConnected: this.forwarderSocket !== null
        };
    }
}