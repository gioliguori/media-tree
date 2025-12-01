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
            forwarderRecoveryCallback: this.notifyRecovery.bind(this)
        });
    }


    async onInitialize() {
    }

    // Avviare il forwarder C -> notifica al controller -> Avviare l'health check
    async onStart() {
        console.log(`[${this.nodeId}] Starting relay node...`);

        await this.forwarder.startForwarder();

        // Notifica Controller che siamo pronti
        await this.notifyReady();

        // PING
        this.forwarder.startHealthCheck();

        console.log(`[${this.nodeId}] Relay node ready`);
    }


    async onStop() {
        console.log(`[${this.nodeId}] Stopping relay node...`);

        // Ferma health check
        this.forwarder.stopHealthCheck();

        // Chiudi socket, termina processo C
        await this.forwarder.shutdown();

        console.log(`[${this.nodeId}] Relay node stopped`);
    }


    // SESSION EVENTS
    /**
     * Handler: session-created
     * Pubblicato su: sessions:{treeId}:{nodeId}
     * Controller invia configurazione solo ai relay coinvolti nel path
     * 
     * Event format:
     * {
     *   type: "session-created",
     *   sessionId: "broadcaster-1",
     *   audioSsrc: 1111,
     *   videoSsrc: 2222,
     *   treeId: "tree-1",
     *   routes: [
     *     {targetId: "egress-1", host: "egress-1", audioPort: 5002, videoPort: 5004},
     *     {targetId: "egress-3", host: "egress-3", audioPort: 5002, videoPort: 5004}
     *   ]
     * }
     */
    async onSessionCreated(event) {
        const { sessionId, audioSsrc, videoSsrc, treeId, routes } = event;

        // Verifica tree
        if (treeId !== this.treeId) {
            console.log(`[${this.nodeId}] Wrong tree: ${treeId} !== ${this.treeId}`);
            return;
        }

        if (!routes || routes.length === 0) {
            console.log(`[${this.nodeId}] No route configured`);
            return;
        }

        try {
            // Aggiungi session al forwarder
            await this.forwarder.addSession(sessionId, audioSsrc, videoSsrc);

            // Aggiungi tutte le route
            for (const route of routes) {
                await this.forwarder.addRoute(
                    sessionId,
                    route.targetId,
                    route.host,
                    route.audioPort,
                    route.videoPort
                );
            }

            console.log(`[${this.nodeId}] Session ${sessionId} configured`);

        } catch (err) {
            console.error(`[${this.nodeId}] Failed to configure session ${sessionId}:`, err.message);
        }
    }
    /**
     * Handler: session-destroyed
     * Pubblicato su: sessions:{treeId}
     * o su: sessions:{treeId}:{nodeId} (ma è raro)
     * i nodi interessati ricevono questo evento per fare cleanup
     * 
     * Event format:
     * {
     *   type: "session-destroyed",
     *   sessionId: "broadcaster-1",
     *   treeId: "tree-1"
     * }
     */
    async onSessionDestroyed(event) {
        const { sessionId, treeId } = event;

        // Verifica tree
        if (treeId !== this.treeId) {
            console.log(`[${this.nodeId}] Wrong tree: ${treeId} !== ${this.treeId}`);
            return;
        }

        try {
            // Rimuovi session dal forwarder (rimuove automaticamente tutte le route)
            await this.forwarder.removeSession(sessionId);

            console.log(`[${this.nodeId}] Session ${sessionId} removed`);

        } catch (err) {
            // Session potrebbe non esistere su questo relay
            if (err.message.includes('not found')) {
                console.log(`[${this.nodeId}] Session ${sessionId} not found (OK)`);
            } else {
                console.error(`[${this.nodeId}] Failed to remove session ${sessionId}:`, err.message);
            }
        }
    }
    /**
     * Handler: route-added
     * Pubblicato su: sessions:{treeId}:{nodeId}
     * Controller aggiunge route
     * 
     * Event format:
     * {
     *   type: "route-added",
     *   sessionId: "broadcaster-1",
     *   treeId: "tree-1",
     *   targetId
     * }
     */
    async onRouteAdded(event) {
        const { sessionId, treeId, targetId } = event;

        if (treeId !== this.treeId) {
            console.log(`[${this.nodeId}] Wrong tree: ${treeId} !== ${this.treeId}`);
            return;
        }

        // relay ha in cache i figli quindi check potrebbe essere valido, ma il controller ha 
        // topologia completa quindi lasciamo a lui la decisione (magari si è verificata race condition)
        // if (!this.children.includes(targetId)) return;

        const route = await this.getNodeInfo(targetId);
        try {
            await this.forwarder.addRoute(
                sessionId,
                targetId,
                route.host,
                route.audioPort,
                route.videoPort
            );

            console.log(`[${this.nodeId}] Route added: ${sessionId} -> ${targetId}`);

        } catch (err) {
            console.error(`[${this.nodeId}] Failed to add route:`, err.message);
        }
    }

    /**
     * Handler: route-removed
     * Pubblicato su: sessions:{treeId}:{nodeId}
     * Controller rimuove route
     * 
     * Event format:
     * {
     *   type: "route-removed",
     *   sessionId: "broadcaster-1",
     *   treeId: "tree-1",
     *   targetId: "egress-1"
     * }
     */
    async onRouteRemoved(event) {
        const { sessionId, treeId, targetId } = event;

        // Verificatree
        if (treeId !== this.treeId) {
            console.log(`[${this.nodeId}] Wrong tree: ${treeId} !== ${this.treeId}`);
            return;
        }

        // if (!this.children.includes(targetId)) return;

        try {
            await this.forwarder.removeRoute(sessionId, targetId);

            console.log(`[${this.nodeId}] Route removed: ${sessionId} -> ${targetId}`);

        } catch (err) {
            console.error(`[${this.nodeId}] Failed to remove route:`, err.message);
        }
    }

    // RECOVERY & NOTIFICATIONS

    /**
     * Notifica Controller che relay è pronto (boot o dopo recovery)
     * Controller deve re-inviare eventi session-created per tutte le session attive
     */
    async notifyReady() {
        console.log(`[${this.nodeId}] Notifying Controller: relay ready`);

        try {
            await this.redis.publish('relay:ready', JSON.stringify({
                type: 'relay-ready',
                nodeId: this.nodeId,
                treeId: this.treeId,
                timestamp: Date.now()
            }));
        } catch (err) {
            console.error(`[${this.nodeId}] Failed to notify ready:`, err.message);
        }
    }

    /**
     * Notifica Controller dopo crash/recovery forwarder
     * Chiamato da RelayForwarderManager quando rileva crash e fa respawn
     */
    async notifyRecovery() {
        console.log(`[${this.nodeId}] Notifying Controller: forwarder recovered`);

        try {
            await this.redis.publish('relay:recovery', JSON.stringify({
                type: 'relay-recovery',
                nodeId: this.nodeId,
                treeId: this.treeId,
                timestamp: Date.now()
            }));
        } catch (err) {
            console.error(`[${this.nodeId}] Failed to notify recovery:`, err.message);
        }
    }


    async getStatus() {
        const baseStatus = await super.getStatus();

        // Ottieni stato forwarder
        let forwarderSessions = null;
        try {
            if (this.forwarder.isReady())
                forwarderSessions = await this.forwarder.listSessions();
        } catch (err) {
            console.error(`[${this.nodeId}] Failed to get sessions:`, err.message);
        }
        const forwarderStatus = this.forwarder.getStatus();
        return {
            ...baseStatus,
            forwarder: {
                running: forwarderStatus.running,
                processAlive: forwarderStatus.processAlive,
                socketConnected: forwarderStatus.socketConnected,
                sessions: forwarderSessions
            }
        };
    }
}