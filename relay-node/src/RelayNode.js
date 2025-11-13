import { BaseNode } from '../shared/BaseNode.js';
import { RelayForwarderManager } from './RelayForwarderManager.js';

export class RelayNode extends BaseNode {

    constructor(config) {
        super(config.nodeId, 'relay', config);

        // RelayForwarderManager per gestire processo C
        this.forwarder = new RelayForwarderManager({
            nodeId: this.nodeId,
            rtpAudioPort: String(this.rtp.audioPort),
            rtpVideoPort: String(this.rtp.videoPort),
            syncCallback: this.syncForwarderState.bind(this)
        });
    }


    async onInitialize() {
    }

    // Avviare il forwarder C -> Sincronizzare i children esistenti -> Avviare l'health check
    async onStart() {
        console.log(`[${this.nodeId}] Starting relay node...`);

        await this.forwarder.startForwarder();

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

        // PING
        this.forwarder.startHealthCheck();

        console.log(`[${this.nodeId}] Relay node ready`);
    }

    // SYNC CALLBACK per RelayForwarderManager
    // forwarderDestinations = Set di "host:audioPort:videoPort" presenti nel forwarder C
    async syncForwarderState(forwarderDestinations) {
        // Ottieni children da Redis
        const childrenInfo = await this.getChildrenInfo();

        // Crea Set di destinations che dovrebbero essere presenti
        const destinations = new Set();
        for (const child of childrenInfo) {
            const key = `${child.host}:${child.audioPort}:${child.videoPort}`;
            destinations.add(key);
        }

        // ADD missing destinations
        for (const key of destinations) {
            if (!forwarderDestinations.has(key)) {
                const [host, audioPort, videoPort] = key.split(':');
                console.log(`[${this.nodeId}] Adding missing destination ${key}`);
                try {
                    await this.forwarder.sendCommand(
                        `ADD ${host} ${audioPort} ${videoPort}`
                    );
                } catch (err) {
                    console.error(`[${this.nodeId}] Failed to add ${key}: `, err.message);
                }
            }
        }

        // REMOVE extra destinations
        for (const key of forwarderDestinations) {
            if (!destinations.has(key)) {
                const [host, audioPort, videoPort] = key.split(':');
                console.log(`[${this.nodeId}] Removing extra destination ${key}`);
                try {
                    await this.forwarder.sendCommand(
                        `REMOVE ${host} ${audioPort} ${videoPort}`
                    );
                } catch (err) {
                    console.error(`[${this.nodeId}] Failed to remove ${key}: `, err.message);
                }
            }
        }
    }

    // @param {string[]} added - ID dei nuovi children
    // @param {string[]} removed - ID dei children rimossi
    async onChildrenChanged(added, removed) {
        console.log(`[${this.nodeId}] children changed: +${added.length} -${removed.length}`);

        if (!this.forwarder.isReady()) {
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
        this.forwarder.stopHealthCheck();

        // Chiudi socket, termina processo C
        await this.forwarder.shutdown();

        console.log(`[${this.nodeId}] Relay node stopped`);
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
        const response = await this.forwarder.sendCommand(`ADD ${host} ${audioPort} ${videoPort}`);

        // Verifica risposta
        if (response !== 'OK') {
            throw new Error(`ADD failed: ${response}`);
        }

        console.log(`[${this.nodeId}] Destination added: ${nodeId} (${host}:${audioPort}/${videoPort})`);
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
        const response = await this.forwarder.sendCommand(`REMOVE ${host} ${audioPort} ${videoPort}`);

        // Verifica risposta
        if (response !== 'OK') {
            throw new Error(`REMOVE failed: ${response}`);
        }

        console.log(`[${this.nodeId}] Destination removed: ${nodeId} (${host}:${audioPort}/${videoPort})`);
    }

    async getStatus() {
        const baseStatus = await super.getStatus();

        // lista delle destinazioni
        let destinations = null;
        try {
            if (this.forwarder.isReady()) {
                destinations = await this.forwarder.listDestinations();
            }
        } catch (err) {
            console.error(`[${this.nodeId}] Failed to get destinations:`, err.message);
        }
        const forwarderStatus = this.forwarder.getStatus();
        return {
            ...baseStatus,
            forwarder: {
                running: forwarderStatus.running,
                processAlive: forwarderStatus.processAlive,
                socketConnected: forwarderStatus.socketConnected,
                destinations
            }
        };
    }
}