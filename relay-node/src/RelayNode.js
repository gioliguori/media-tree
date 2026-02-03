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


    async onSessionCreated(event) {
        const { sessionId, audioSsrc, videoSsrc, routes } = event;
        console.log(`[${this.nodeId}] Handling session-created for ${sessionId} with ${routes?.length || 0} routes`);

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
    async onSessionDestroyed(event) {
        const { sessionId } = event;

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
    async onRouteAdded(event) {
        const { sessionId, targetId } = event;

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

    async onRouteRemoved(event) {
        const { sessionId, targetId } = event;

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
    async getMetrics() {
        const baseMetrics = await super.getMetrics();

        let gstreamerStats = null;

        try {
            if (this.forwarder.isReady()) {
                gstreamerStats = await this.forwarder.getStats();
            }
        } catch (err) {
            console.error(`[${this.nodeId}] Failed to get GStreamer stats:`, err.message);
        }

        return {
            ...baseMetrics,
            gstreamer: gstreamerStats
            // Output esempio: 
            // {
            //   maxAudioQueueMs: 45.3,
            //   maxVideoQueueMs: 120.5,
            //   sessionCount:  3,
            //   sessions: [...]
            // }
        };
    }
}