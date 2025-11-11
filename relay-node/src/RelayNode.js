import { BaseNode } from '../shared/BaseNode.js';
import { spawn } from 'child_process';
import net from 'net';                      // per socket unix
import path from 'path';
import fs from 'fs/promises';               // filesystem promise per check 

export class RelayNode extends BaseNode {

    constructor(config) {
        super(config.nodeId, 'relay', config);

        // process.cwd() = directory corrente (in Docker /app)
        this.forwarderPath = path.join(process.cwd(), 'forwarder', 'relay-forwarder');

        // percorso socket
        this.socketPath = `/tmp/relay-forwarder-${this.nodeId}.sock`;

        // riferimenti al processo C e alla socket
        this.forwarderProcess = null;
        this.forwarderSocket = null;
        this.isForwarderReady = false;

        // Health check processo C
        this.healthCheckInterval = null;

        // COMMAND TRACKING
        // Map per tracciare i comandi in attesa di risposta: commandId -> { cmd, resolve, reject, buffer }
        this.pendingCommands = new Map();

        // Counter ID comandi
        this.commandId = 0;
    }

    // Avviare il forwarder C -> Sincronizzare i children esistenti -> Avviare l'health check
    async onInitialize() {
        console.log(`[${this.nodeId}] Initializing relay node...`);

        await this.startForwarder();

        // controlla se ci sono già children registrati in Redis
        const childrenInfo = await this.getChildrenInfo();

        if (childrenInfo.length > 0) {
            console.log(`[${this.nodeId}] Found ${childrenInfo.length} children`);

            // Aggiungi ogni child al forwarder
            for (const child of childrenInfo) {
                try {
                    await this.addDestination(child);
                } catch (err) {
                    console.error(`[${this.nodeId}] Failed to add child ${child.nodeId}:`, err.message);
                }
            }
        } else {
            console.log(`[${this.nodeId}] No children yet`);
        }

        // PING ogni 30s
        this.startHealthCheck();

        console.log(`[${this.nodeId}] Relay node ready`);
    }

    // @param {string[]} added - ID dei nuovi children
    // @param {string[]} removed - ID dei children rimossi
    async onChildrenChanged(added, removed) {
        console.log(`[${this.nodeId}] children changed: +${added.length} -${removed.length}`);

        if (!this.isForwarderReady) {
            console.warn(`[${this.nodeId}] Forwarder not ready`);
            return;
        }

        // AGGIUNGI NUOVI CHILDREN
        for (const childId of added) {
            try {
                // Recupera info da Redis
                const info = await this.getNodeInfo(childId);
                if (info) {
                    // Invia comando ADD al forwarder C
                    await this.addDestination(info);
                } else {
                    console.warn(`[${this.nodeId}] Child ${childId} info not found`);
                }
            } catch (err) {
                console.error(`[${this.nodeId}] Failed to add child ${childId}:`, err.message);
            }
        }

        // RIMUOVI CHILDREN 
        for (const childId of removed) {
            try {
                // Recupera info da Redis
                const info = await this.getNodeInfo(childId);
                if (info) {
                    // Invia comando REMOVE al forwarder C
                    await this.removeDestination(info);
                } else {
                    // Child già cancellato da Redis, non sappiamo host/porte                
                    console.warn(`[${this.nodeId}] Cannot remove ${childId}: info not found`);
                }
            } catch (err) {
                console.error(`[${this.nodeId}] Failed to remove child ${childId}:`, err.message);
            }
        }
    }

    async onStop() {
        console.log(`[${this.nodeId}] Stopping relay node...`);

        // Ferma health check
        if (this.healthCheckInterval) {
            clearInterval(this.healthCheckInterval);
            this.healthCheckInterval = null;
        }

        // chiudi socket
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

        // termina processo C
        if (this.forwarderProcess) {
            this.forwarderProcess.kill('SIGTERM');
            this.forwarderProcess = null;
        }

        console.log(`[${this.nodeId}] Relay node stopped`);
    }

    async startForwarder() {
        console.log(`[${this.nodeId}] Starting forwarder...`);
        console.log(`[${this.nodeId}] Socket: ${this.socketPath}`);
        console.log(`[${this.nodeId}] Ports: audio=${this.rtp.audioPort} video=${this.rtp.videoPort}`);

        // spawn(path dell'eseguibile, [argomenti], opzioni) 
        this.forwarderProcess = spawn(this.forwarderPath, [
            this.nodeId,                      // argv[1]
            String(this.rtp.audioPort),       // argv[2]
            String(this.rtp.videoPort)        // argv[3]
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

        // GESTIONE EXIT PROCESSO C 
        this.forwarderProcess.on('exit', (code, signal) => {
            console.error(`[${this.nodeId}] Forwarder exited: code=${code} signal=${signal}`);
            this.isForwarderReady = false;

            // TODO: auto-restart
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

        // PING di verifica
        const response = await this.sendCommand('PING');
        if (response !== 'PONG') {
            throw new Error(`Unexpected PING response: ${response}`);
        }

        console.log(`[${this.nodeId}] Forwarder ready`);
    }

    // Controlla ogni 100ms se il file esiste, max 10 secondi
    async waitForSocket() {
        const maxWait = 10000;      // 10 secondi
        const checkInterval = 100;   // controlla ogni 100ms
        const startTime = Date.now();

        // console.log(`[${this.nodeId}] Waiting for socket...`);

        // Loop: controlla se file socket esiste
        while (Date.now() - startTime < maxWait) {
            try {
                // fs.access() lancia errore se file non esiste
                await fs.access(this.socketPath);

                //console.log(`[${this.nodeId}] Socket found`);
                return;

            } catch (err) {
                // Socket non ancora pronto, aspetta 100ms
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

            // EVENTI

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
            // dati parziali verranno processati al prossimo 'data' event
        });
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
            // Risolvi la Promise
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
            // Aggiungi la riga al buffer
            pending.buffer.push(line);
        }
    }


    // DESTINATION MANAGEMENT

    async addDestination(child) {
        const { nodeId, host, audioPort, videoPort } = child;

        if (!nodeId || !host || !audioPort || !videoPort) {
            console.error(`[${this.nodeId}] Invalid child info:`, child);
            throw new Error('Invalid child parameters');
        }
        // Invia comando ADD al forwarder C
        // Formato: "ADD <host> <audioPort> <videoPort>"
        const response = await this.sendCommand(`ADD ${host} ${audioPort} ${videoPort}`);

        // Verifica risposta
        if (response !== 'OK') {
            throw new Error(`ADD failed: ${response}`);
        }

        console.log(`[${this.nodeId}] Destination added: ${nodeId}`);
    }

    async removeDestination(child) {
        const { nodeId, host, audioPort, videoPort } = child;
        // console.log(`[${this.nodeId}] Removing destination: ${nodeId} (${host}:${audioPort}/${videoPort})`);
        if (!nodeId || !host || !audioPort || !videoPort) {
            console.error(`[${this.nodeId}] Invalid child info:`, child);
            throw new Error('Invalid child parameters');
        }
        // Invia comando REMOVE al forwarder C
        // Formato: "REMOVE <host> <audioPort> <videoPort>"
        const response = await this.sendCommand(`REMOVE ${host} ${audioPort} ${videoPort}`);

        // Verifica risposta
        if (response !== 'OK') {
            throw new Error(`REMOVE failed: ${response}`);
        }

        console.log(`[${this.nodeId}] Destination removed: ${nodeId}`);
    }

    async listDestinations() {
        // Invia comando LIST al forwarder C
        // Risposta formato:
        // AUDIO: host1:port1,host2:port2
        // VIDEO: host1:port1,host2:port2
        // END
        const response = await this.sendCommand('LIST');
        return response;
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

            } catch (err) {
                // PING fallito
                console.error(`[${this.nodeId}] Health check failed:`, err.message);

                // Setta flag a false
                this.isForwarderReady = false;

                // TODO: auto-restart
            }
        }, 30000);  // 30 secondi
    }

    async getStatus() {
        const baseStatus = await super.getStatus();

        // lista delle destinazioni
        let destinations = null;
        try {
            if (this.isForwarderReady) {
                destinations = await this.listDestinations();
            }
        } catch (err) {
            console.error(`[${this.nodeId}] Failed to get destinations:`, err.message);
        }
        return {
            ...baseStatus,
            forwarder: {
                running: this.isForwarderReady,
                processAlive: this.forwarderProcess !== null,
                socketConnected: this.forwarderSocket !== null,
                destinations
            }
        };
    }
}