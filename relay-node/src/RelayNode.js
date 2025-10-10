import { BaseNode } from '../shared/BaseNode.js';
import dgram from 'dgram';

export class RelayNode extends BaseNode {
    constructor(config) {
        super(config.nodeId, 'relay', config);

        // UDP sockets
        this.audioSocket = null;
        this.videoSocket = null;

        // cache
        this.childrenTargets = [];

        // Stats
        this.stats = {
            audioPackets: 0,
            videoPackets: 0
        };
    }

    async onInitialize() {
        console.log(`[${this.nodeId}] Initializing UDP sockets...`);

        // audio
        this.audioSocket = dgram.createSocket('udp4');
        this.audioSocket.bind(this.rtp.audioPort, '0.0.0.0');
        this.audioSocket.on('message', (msg) => this.forwardAudioPacket(msg));
        this.audioSocket.on('error', (err) => {
            console.error(`[${this.nodeId}] Audio socket error:`, err);
        });
        // video
        this.videoSocket = dgram.createSocket('udp4');
        this.videoSocket.bind(this.rtp.videoPort, '0.0.0.0');
        this.videoSocket.on('message', (msg) => this.forwardVideoPacket(msg));
        this.videoSocket.on('error', (err) => {
            console.error(`[${this.nodeId}] Video socket error:`, err);
        });

        console.log(`[${this.nodeId}] Listening on UDP ${this.rtp.audioPort}/${this.rtp.videoPort}`);
    }

    async onParentChanged(oldParent, newParent) {
        //    Se cambia il padre cosa deve fare? niente credo..
        //    in realtà se dobbiamo supportare piu sessioni, c'è da pensare
        //    if (!newParent) {
        //        console.log(`[${this.nodeId}] No parent assigned yet`);
        //        return;
        //    }
        //
        //    const parentInfo = await this.getParentInfo();
        //    if (!parentInfo) {
        //        console.error(`[${this.nodeId}] Parent ${newParent} not found in Redis`);
        //        return;
        //    }
        //   
        //    console.log(`[${this.nodeId}] Now expecting RTP from ${parentInfo.host}`);
    }

    async onChildrenChanged(added, removed) {
        // Rimuovi vecchi
        removed.forEach(childId => {
            const index = this.childrenTargets.findIndex(t => t.id === childId);
            if (index !== -1) {
                const [removedChild] = this.childrenTargets.splice(index, 1);
                console.log(`  ${childId} (${removedChild.host})`);
            }
        });

        // Aggiungi nuovi
        if (added.length > 0) {
            const infos = await Promise.all(added.map(id => this.getNodeInfo(id)));

            added.forEach((childId, i) => {
                const info = infos[i];
                if (!info) return console.error(` ${childId} not found`);

                this.childrenTargets.push({
                    id: childId,
                    host: info.host,
                    audioPort: info.audioPort, // no parseint, giò lo fa di la.
                    videoPort: info.videoPort
                });
                console.log(` ${childId} (${info.host}:${info.audioPort})`);
            });
        }

        console.log(`[${this.nodeId}] Forwarding to ${this.childrenTargets.length} children`);
    }

    forwardAudioPacket(packet) {
        this.stats.audioPackets++;

        for (const target of this.childrenTargets) {
            this.audioSocket.send(
                packet,
                target.audioPort,
                target.host,
                (err) => {
                    if (err) console.error(`[${this.nodeId}] Audio forward error to ${target.id}:`, err.message);
                }
            );
        }
    }

    forwardVideoPacket(packet) {
        this.stats.videoPackets++;

        for (const target of this.childrenTargets) {
            this.videoSocket.send(
                packet,
                target.videoPort,
                target.host,
                (err) => {
                    if (err) console.error(`[${this.nodeId}] Video forward error to ${target.id}:`, err.message);
                }
            );
        }
    }

    async onStop() {
        if (this.audioSocket) this.audioSocket.close();
        if (this.videoSocket) this.videoSocket.close();
    }

    async getStatus() {
        const baseStatus = await super.getStatus();
        return {
            ...baseStatus,
            stats: this.stats,
            forwarding: {
                childrenCount: this.childrenTargets.length,
                targets: this.childrenTargets.map(t => `${t.id} (${t.host})`)
            }
        };
    }
}