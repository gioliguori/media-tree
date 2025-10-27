import { BaseNode } from '../shared/BaseNode.js';
import { PortPool } from '../shared/PortPool.js';
import { saveMountpointToRedis, deactivateMountpointInRedis, getMountpointInfo, getAllMountpointsInfo } from './mountpoint-utils.js';
import { connectToJanusStreaming, createJanusMountpoint, destroyJanusMountpoint } from './janus-streaming-utils.js';

import { JanusWhepServer } from 'janus-whep-server';
import { spawn } from 'child_process';
import express from 'express';

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
        this.mountpoints = new Map(); // sessionId → mountpointData
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

        // GStreamer pipelines
        this.audioPipeline = null;
        this.videoPipeline = null;

        // Pipeline health
        this.pipelineHealth = {
            audio: { running: false, restarts: 0, lastError: null },
            video: { running: false, restarts: 0, lastError: null }
        };

        // Lock per operazioni PER SESSIONE (Map)
        this.operationLocks = new Map();

        // Lock per rebuild GLOBALE (boolean)
        this.isRebuilding = false;
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
        console.log(`[${this.nodeId}] Egress node ready (no pipelines started yet)`);
    }

    async onStop() {
        console.log(`[${this.nodeId}] Stopping egress node...`);

        // Distruggi tutti i mountpoint 
        const sessionIds = Array.from(this.mountpoints.keys());
        console.log(`[${this.nodeId}] Destroying ${sessionIds.length} active mountpoints...`);
        // non funziona per il momento
        //for (const sessionId of sessionIds) {
        //    try {
        //        await this.destroyMountpoint(sessionId);
        //    } catch (error) {
        //        console.error(`[${this.nodeId}] Error destroying mountpoint ${sessionId}:`, error.message);
        //    }
        //}

        // Stop GStreamer pipelines
        this.stopPipelines();

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

    // ============ MOUNTPOINT MANAGEMENT ============

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

            console.log(`[${this.nodeId}] Mountpoint created: ${sessionId}`);
            console.log(`[${this.nodeId}] WHEP endpoint: ${this.whepBasePath}/endpoint/${sessionId}`);

            // Rebuild pipelines con nuovo mountpoint
            await this.rebuildPipelines();

            return {
                sessionId,
                mountpointId,
                whepUrl: `${this.whepBasePath}/endpoint/${sessionId}`,
                janusAudioPort: audioPort,
                janusVideoPort: videoPort
            };
        } finally {
            this.operationLocks.delete(sessionId);
        }
    }
    // problematica attualmente
    // async destroyMountpoint(sessionId) {
    //     // check esistenza
    //     if (!this.mountpoints.has(sessionId)) {
    //         throw new Error(`Mountpoint for session ${sessionId} not found`);
    //     }
    //     // lock
    //     if (this.operationLocks.get(sessionId)) {
    //         throw new Error(`Operation already in progress for session ${sessionId}`);
    //     }
    //     this.operationLocks.set(sessionId, true);

    //     try {
    //         console.log(`[${this.nodeId}] Destroying mountpoint for session: ${sessionId}`);

    //         const mountpoint = this.mountpoints.get(sessionId);
    //         const { mountpointId, janusAudioPort, janusVideoPort, endpoint } = mountpoint;

    //         // memoria locale
    //         this.mountpoints.delete(sessionId);

    //         // Rilascia porte al pool
    //         this.portPool.release(janusAudioPort, janusVideoPort);
    //         console.log(`[${this.nodeId}] Released ports ${janusAudioPort}, ${janusVideoPort} to pool`);

    //         // rimuove dopo 24 ore
    //         await deactivateMountpointInRedis(this.redis, this.nodeId, mountpointId);

    //         try {
    //             if (this.whepServer && endpoint) {
    //                 this.whepServer.destroyEndpoint(sessionId);
    //                 console.log(`[${this.nodeId}] WHEP endpoint ${sessionId} destroyed`);
    //             }
    //         } catch (error) {
    //             console.error(`[${this.nodeId}] Error destroying WHEP endpoint:`, error.message);
    //             // Non bloccare - continua con cleanup
    //         }

    //         await destroyJanusMountpoint(this.janusStreaming, this.nodeId, mountpointId, this.mountpointSecret);

    //         console.log(`[${this.nodeId}] Mountpoint destroyed: ${sessionId}`);

    //         // Rebuild pipelines senza questo mountpoint
    //         await this.rebuildPipelines();

    //         return { sessionId, mountpointId };

    //     } catch (error) {
    //         console.error(`[${this.nodeId}] Error destroying mountpoint ${sessionId}:`, error.message);
    //         throw error;
    //     } finally {
    //         this.operationLocks.delete(sessionId);
    //     }
    // }

    getMountpoint(sessionId) {
        return getMountpointInfo(this.mountpoints, sessionId);
    }

    getAllMountpoints() {
        return getAllMountpointsInfo(this.mountpoints);
    }

    // ============ GSTREAMER PIPELINE MANAGEMENT ============

    async rebuildPipelines() {
        // lock
        if (this.isRebuilding) {
            console.log(`[${this.nodeId}] Rebuild already in progress`);
            return;
        }
        this.isRebuilding = true;

        try {
            console.log(`[${this.nodeId}] Rebuilding GStreamer pipelines...`);

            // Stop pipeline correnti
            this.stopPipelines();

            // Leggi mountpoint attivi dalla memoria, per il momento funziona, se però crasha nodo
            // bisogna prenderli da redis (TODO), -> aggiungere in mem, ricreare endpoints, riallocare porte...
            // magari quando parte il nodo, facciamo chiamata a vuoto però risolve
            const activeMountpoints = Array.from(this.mountpoints.values())
                .filter(item => item.active)
                .map(item => ({
                    mountpointId: item.mountpointId,
                    audioSsrc: item.audioSsrc,
                    videoSsrc: item.videoSsrc,
                    janusAudioPort: item.janusAudioPort,
                    janusVideoPort: item.janusVideoPort
                }));

            if (activeMountpoints.length === 0) {
                console.log(`[${this.nodeId}] No active mountpoints, pipelines stopped`);
                return;
            }

            console.log(`[${this.nodeId}] Starting pipelines for ${activeMountpoints.length} mountpoints`);

            // Start nuove pipeline
            this._startAudioPipeline(activeMountpoints);
            this._startVideoPipeline(activeMountpoints);

            console.log(`[${this.nodeId}] Pipelines rebuilt successfully`);

        } catch (error) {
            console.error(`[${this.nodeId}] Error rebuilding pipelines:`, error.message);
            this.pipelineHealth.audio.lastError = error.message;
            this.pipelineHealth.video.lastError = error.message;
            throw error;
        } finally {
            this.isRebuilding = false;
        }
    }

    stopPipelines() {
        if (this.audioPipeline) {
            try {
                this.audioPipeline.kill('SIGTERM');
                console.log(`[${this.nodeId}] Audio pipeline stopped`);
            } catch (error) {
                console.error(`[${this.nodeId}] Error stopping audio pipeline:`, error.message);
            }
            this.audioPipeline = null;
            this.pipelineHealth.audio.running = false;
        }

        if (this.videoPipeline) {
            try {
                this.videoPipeline.kill('SIGTERM');
                console.log(`[${this.nodeId}] Video pipeline stopped`);
            } catch (error) {
                console.error(`[${this.nodeId}] Error stopping video pipeline:`, error.message);
            }
            this.videoPipeline = null;
            this.pipelineHealth.video.running = false;
        }
    }

    _startAudioPipeline(mountpoints) {
        // comando con rtpssrcdemux
        // this.rtp.audioPort da base node
        let cmd = `gst-launch-1.0 -v udpsrc port=${this.rtp.audioPort} caps="application/x-rtp,media=audio,encoding-name=OPUS,clock-rate=48000,payload=111" ! rtpssrcdemux name=demux_audio`; //  ! rtpjitterbuffer devo ancora capire se funziona

        mountpoints.forEach(mp => {
            cmd += ` demux_audio.src_${mp.audioSsrc} ! queue ! udpsink host=${this.rtpDestinationHost} port=${mp.janusAudioPort}`;
        });

        this.pipelineHealth.audio.restarts++;
        console.log(`[${this.nodeId}] Starting audio pipeline #${this.pipelineHealth.audio.restarts})`);
        // console.log(`[${this.nodeId}] Audio command: ${cmd}`);

        this.audioPipeline = spawn('sh', ['-c', cmd]);

        this.pipelineHealth.audio.running = true;
        this.pipelineHealth.audio.lastError = null;

        this._attachPipelineHandlers('audio', this.audioPipeline, () => {
            // Callback per auto-restart
            if (!this.isStopping && this.mountpoints.size > 0) {
                const activeMountpoints = Array.from(this.mountpoints.values())
                    .filter(item => item.active)
                    .map(item => ({
                        mountpointId: item.mountpointId,
                        audioSsrc: item.audioSsrc,
                        videoSsrc: item.videoSsrc,
                        janusAudioPort: item.janusAudioPort,
                        janusVideoPort: item.janusVideoPort
                    }));

                this._startAudioPipeline(activeMountpoints);
            }
        });
    }

    _startVideoPipeline(mountpoints) {
        // Costruisci comando con rtpssrcdemux
        // this.rtp.audioPort da base node
        let cmd = `gst-launch-1.0 -v udpsrc port=${this.rtp.videoPort} caps="application/x-rtp,media=video,encoding-name=VP8,clock-rate=90000,payload=96" ! rtpssrcdemux name=demux_video`; //  ! rtpjitterbuffer

        mountpoints.forEach(mp => {
            cmd += ` demux_video.src_${mp.videoSsrc} ! queue ! udpsink host=${this.rtpDestinationHost} port=${mp.janusVideoPort}`;
        });

        this.pipelineHealth.video.restarts++;
        console.log(`[${this.nodeId}] Starting video pipeline #${this.pipelineHealth.video.restarts})`);
        // console.log(`[${this.nodeId}] Video command: ${cmd}`);

        this.videoPipeline = spawn('sh', ['-c', cmd]);

        this.pipelineHealth.video.running = true;
        this.pipelineHealth.video.lastError = null;

        this._attachPipelineHandlers('video', this.videoPipeline, () => {
            // Callback per auto-restart
            if (!this.isStopping && this.mountpoints.size > 0) {
                const activeMountpoints = Array.from(this.mountpoints.values())
                    .filter(item => item.active)
                    .map(item => ({
                        mountpointId: item.mountpointId,
                        audioSsrc: item.audioSsrc,
                        videoSsrc: item.videoSsrc,
                        janusAudioPort: item.janusAudioPort,
                        janusVideoPort: item.janusVideoPort
                    }));

                this._startVideoPipeline(activeMountpoints);
            }
        });
    }

    // ============ API ENDPOINTS ============

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

                const result = await this.createMountpoint(sessionId, parseInt(audioSsrc), parseInt(videoSsrc), this.mountpointSecret);
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

        // GET /pipelines (debug info)
        this.app.get('/pipelines', (req, res) => {
            try {
                const activeCount = Array.from(this.mountpoints.values())
                    .filter(item => item.active).length;
                res.json({
                    audio: {
                        running: this.pipelineHealth.audio.running,
                        restarts: this.pipelineHealth.audio.restarts,
                        lastError: this.pipelineHealth.audio.lastError
                    },
                    video: {
                        running: this.pipelineHealth.video.running,
                        restarts: this.pipelineHealth.video.restarts,
                        lastError: this.pipelineHealth.video.lastError
                    },
                    mountpointsActive: activeCount
                });
            } catch (error) {
                console.error(`[${this.nodeId}] Get pipelines error:`, error.message);
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
    }

    async getStatus() {
        const baseStatus = await super.getStatus();
        const activeCount = Array.from(this.mountpoints.values())
            .filter(item => item.active).length;
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
            gstreamer: this.pipelineHealth,
            mountpoints: {
                active: activeCount,
                list: this.getAllMountpoints()
            },
            portPool: this.portPool.getStats()
        };
    }
}