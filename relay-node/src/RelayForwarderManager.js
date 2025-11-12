import { spawn } from 'child_process';
import net from 'net';                      // per socket unix
import path from 'path';
import fs from 'fs/promises';               // filesystem promise per check 

export class RelayForwarderManager {
    constructor(config) {
        this.nodeId = config.nodeId;
        this.rtpAudioPort = config.rtpAudioPort;
        this.rtpVideoPort = config.rtpVideoPort;

        // Percorso socket Unix
        this.socketPath = `/tmp/relay-forwarder-${this.nodeId}.sock`;

        // process.cwd() = directory corrente (in Docker /app)
        this.forwarderPath = path.join(process.cwd(), 'forwarder', 'relay-forwarder');

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

        // Callback per sync state con destinations
        this.syncCallback = config.syncCallback || null;
    }

    async startForwarder() {
        console.log(`[${this.nodeId}] Starting forwarder...`);
        console.log(`[${this.nodeId}] Socket: ${this.socketPath}`);
        console.log(`[${this.nodeId}] Ports: audio=${this.rtpAudioPort} video=${this.rtpVideoPort}`);

        // spawn (path dell'eseguibile, [argomenti], opzioni) 
        this.forwarderProcess = spawn(this.forwarderPath, [
            this.nodeId,                      // argv[1]
            String(this.rtpAudioPort),       // argv[2]
            String(this.rtpVideoPort)        // argv[3]
        ], {
            // stdio: configurazione input/output
            // 'ignore' = stdin chiuso
            // 'pipe' = stdout/stderr redirect
            stdio: ['ignore', 'pipe', 'pipe'],
        });

        // stdout del processo C
        this.forwarderProcess.stdout.on('data', (data) => {
            const msg = data.toString().trim();
            if (msg) {
                console.log(`[${this.nodeId}] [C] ${msg}`);
            }
        });

        // stderr del processo C
        this.forwarderProcess.stderr.on('data', (data) => {
            const msg = data.toString().trim();
            if (msg) {
                console.error(`[${this.nodeId}] [C ERROR] ${msg}`);
            }
        });

        // gestione exit processo C 
        this.forwarderProcess.on('exit', (code, signal) => {
            console.error(`[${this.nodeId}] Forwarder exited: code=${code} signal=${signal}`);
            this.isForwarderReady = false;
        });

        // Errore spawn
        this.forwarderProcess.on('error', (err) => {
            console.error(`[${this.nodeId}] Failed to spawn forwarder:`, err.message);
            this.isForwarderReady = false;
        });

        // aspetta il socket
        await this.waitForSocket();
        // connetti
        await this.connectToSocket();

        this.isForwarderReady = true;

        // PING
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
        const maxWait = 10000;      // 10 secondi
        const checkInterval = 100;
        const startTime = Date.now();

        // console.log(`[${this.nodeId}] Waiting for socket...`);

        while (Date.now() - startTime < maxWait) {
            try {
                // fs.access() lancia errore se file non esiste
                await fs.access(this.socketPath);
                //console.log(`[${this.nodeId}] Socket found`);
                return;
            } catch (err) {
                // aspetta 100ms
                await new Promise(resolve => setTimeout(resolve, checkInterval));
            }
        }
        throw new Error(`Socket timeout: ${this.socketPath} not created after ${maxWait}ms`);
    }

    // ritorna una promise che si risolve quando la connessione è stabilita
    async connectToSocket() {
        // promise per gestire perché net.createConnection() usa eventi
        return new Promise((resolve, reject) => {

            // crea connessione socket
            this.forwarderSocket = net.createConnection(this.socketPath);

            // Connessione stabilita
            this.forwarderSocket.on('connect', () => {
                //console.log(`[${this.nodeId}] Socket connected`);
                resolve();  // promise completata
            });

            // Errore connessione
            this.forwarderSocket.on('error', (err) => {
                console.error(`[${this.nodeId}] Socket error:`, err.message);
                reject(err);  // promise fallita
            });

            // Socket chiuso (processo C fallito o disconnect)
            this.forwarderSocket.on('close', () => {
                console.log(`[${this.nodeId}] Socket closed`);
                this.isForwarderReady = false;
            });

            // Setup handler per leggere risposte dal processo C
            this.setupSocketDataHandler();

            // timeout 10 secondi
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
            // dati parziali verranno processati al prossimo 'data' event
        });
    }

    handleSocketResponse(line) {
        if (!line) return;

        // console.log(`[${this.nodeId}] [Response] ${line}`);

        // Prendi primo comando in attesa
        const firstEntry = this.pendingCommands.entries().next().value;

        if (!firstEntry) {
            console.warn(`[${this.nodeId}]  Unexpected response: ${line}`);
            return;
        }

        const [id, pending] = firstEntry;  // [0, { cmd, resolve, reject }]

        // gestisci risposta

        // Risposte semplici: OK, PONG, BYE
        if (line === 'OK' || line === 'PONG' || line === 'BYE') {
            // Rimuovi dalla lista pending
            this.pendingCommands.delete(id);
            pending.resolve(line);
        }
        // risposta multiriga
        else if (line === 'END') {
            this.pendingCommands.delete(id);
            // Restituisci buffer accumulato
            pending.resolve(pending.buffer ? pending.buffer.join('\n') : '');
        }
        // Risposta errore
        else if (line.startsWith('ERROR')) {
            this.pendingCommands.delete(id);
            pending.reject(new Error(line));
        } else {
            // Accumula righe in un buffer
            if (!pending.buffer) pending.buffer = [];
            pending.buffer.push(line);
        }
    }

    // @param {string} cmd - Comando (es: "PING", "ADD relay-2 5002 5004")
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

            // Quando arriva la risposta, handleSocketResponse() chiama resolve/reject
            this.pendingCommands.set(id, {
                cmd,
                resolve: (value) => {
                    clearTimeout(timeout);  // cancella il timeout
                    resolve(value);         // risolvi la Promise
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
            // Ottieni stato attuale dal forwarder
            const forwarderState = await this.listDestinations();
            if (!forwarderState) {
                console.error(`[${this.nodeId}] Cannot sync: forwarder LIST failed`);
                return;
            }
            // Parse delle destinazioni dal forwarder
            // Formato: "AUDIO: host1:port1,host2:port2\nVIDEO: host1:port1,host2:port2" non va bene
            const forwarderDestinations = this.parseDestinations(forwarderState);

            // Usa callback per ottenere destinations da RelayNode
            await this.syncCallback(forwarderDestinations);

            // console.log(`[${this.nodeId}] Sync completed`);

        } catch (err) {
            console.error(`[${this.nodeId}] Sync error: `, err.message);
        }
    }

    // Parse della risposta LIST
    // Input: "AUDIO: host1:port1,host2:port2\nVIDEO: host1:port1,host2:port2"
    // Output: Set di "host:audioPort:videoPort"
    // serve per sync
    parseDestinations(response) {
        const destinations = new Set();

        const lines = response.split('\n');
        let audioDestinations = [];
        let videoDestinations = [];

        for (const line of lines) {
            if (line.startsWith('AUDIO: ')) {
                const clients = line.substring(7).trim(); // Rimuovi "AUDIO: "
                if (clients && clients !== '(none)') {
                    audioDestinations = clients.split(',').map(c => c.trim());
                }
            } else if (line.startsWith('VIDEO: ')) {
                const clients = line.substring(7).trim(); // Rimuovi "VIDEO: "
                if (clients && clients !== '(none)') {
                    videoDestinations = clients.split(',').map(c => c.trim());
                }
            }
        }

        // Le destinazioni devono essere presenti sia in audio che video
        // chiave unica: "host:audioPort:videoPort" così il comando ADD/REMOVE la accetta
        for (const audioDest of audioDestinations) {
            const [audioHost, audioPort] = audioDest.split(':');

            // Trova video corrispondente (stesso host)
            for (const videoDest of videoDestinations) {
                const [videoHost, videoPort] = videoDest.split(':');

                if (audioHost === videoHost) {
                    // Aggiungi destination completa
                    destinations.add(`${audioHost}:${audioPort}:${videoPort}`);
                }
            }
        }

        return destinations;
    }

    // HEALTH CHECK
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

                // Sincronizzazione
                await this.syncForwarder();
            } catch (err) {
                // PING fallito
                console.error(`[${this.nodeId}] Health check failed:`, err.message);

                // Setta flag a false
                this.isForwarderReady = false;

                // Auto-restart forwarder
                try {
                    await this.killForwarder();
                    // Wait
                    await new Promise(resolve => setTimeout(resolve, 500));

                    await this.startForwarder();

                    // Sincronizzazione dopo restart
                    await this.syncForwarder();

                    console.log(`[${this.nodeId}] Forwarder recovered successfully`);
                } catch (respawnErr) {
                    console.error(`[${this.nodeId}] Respawn failed:`, respawnErr.message);
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

    async listDestinations() {
        try {
            const response = await this.sendCommand('LIST');
            return response;
        } catch (err) {
            console.error(`[${this.nodeId}] Failed to list destinations: `, err.message);
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
            // Chiudi la connessione socket
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