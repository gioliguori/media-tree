// EgressNode.js
import { BaseNode } from '../shared/BaseNode.js';
import { JanusWhepServer } from 'janus-whep-server';
import express from 'express';
import { spawn } from 'child_process';

export class EgressNode extends BaseNode {
    constructor(config) {
        super(config.nodeId, 'egress', config);

        // WHEP Server
        this.whepServer = null;
        this.janusUrl = config.janus.streaming.wsUrl;
        this.whepBasePath = config.whep?.basePath || '/whep';
        this.whepToken = config.whep?.token || 'verysecret';

        // GStreamer pipelines
        this.audioPipeline = null;
        this.videoPipeline = null;

        // Pipeline health monitoring
        this.pipelineHealth = {
            audio: { running: false, restarts: 0, lastError: null },
            video: { running: false, restarts: 0, lastError: null }
        };

        // Destinazione RTP (deriva da config esistente)
        this.rtpDestination = {
            host: new URL(this.janusUrl).hostname,  // 'janus-streaming'
            audioPort: this.rtp.audioPort,          // 5002
            videoPort: this.rtp.videoPort           // 5004
        };

        // Sessione
        this.currentSession = {
            sessionId: null,
            mountpointId: null,
            createdAt: null,
            active: false
        };

    }

    async onInitialize() {
        console.log(`[${this.nodeId}] Initializing WHEP server...`);

        this.whepServer = new JanusWhepServer({
            janus: { address: this.janusUrl },
            rest: { app: this.app, basePath: this.whepBasePath }
        });

        // Start GStreamer forwarding
        this._startRtpForwarding();

        // File statici
        this.app.use(express.static('web'));

        console.log(`[${this.nodeId}] WHEP server initialized`);
    }

    async onStart() {
        console.log(`[${this.nodeId}] Starting WHEP server...`);
        await this.whepServer.start();
        console.log(`[${this.nodeId}] WHEP server listening on ${this.whepBasePath}`);
    }

    async onStop() {
        console.log(`[${this.nodeId}] Stopping egress node...`);

        if (this.currentSession.active) {
            try {
                await this.destroySession();
            } catch (error) {
                console.error(`[${this.nodeId}] Error destroying session:`, error.message);
            }
        }

        // Stop pipelines
        if (this.audioPipeline) {
            console.log(`[${this.nodeId}] Stopping audio pipeline...`);
            this.audioPipeline.kill('SIGTERM');
        }
        if (this.videoPipeline) {
            console.log(`[${this.nodeId}] Stopping video pipeline...`);
            this.videoPipeline.kill('SIGTERM');
        }

        // Destroy WHEP server
        if (this.whepServer) {
            try {
                await this.whepServer.destroy();
                console.log(`[${this.nodeId}] WHEP server destroyed`);
            } catch (error) {
                console.error(`[${this.nodeId}] Error destroying WHEP server:`, error.message);
            }
        }

        console.log(`[${this.nodeId}] Egress node stopped`);
    }

    // ============ TOPOLOGY ============

    async onParentChanged(oldParent, newParent) {
        console.log(`[${this.nodeId}] Parent changed: ${oldParent} → ${newParent}`);

        if (newParent) {
            const parentInfo = await this.getNodeInfo(newParent);

            if (parentInfo) {
                console.log(`[${this.nodeId}] New parent: ${parentInfo.host}:${parentInfo.audioPort}/${parentInfo.videoPort}`);
            } else {
                console.warn(`[${this.nodeId}] Parent ${newParent} not found in Redis`);
            }
        } else {
            console.log(`[${this.nodeId}] No parent assigned`);
        }
    }

    // ============ SESSION MANAGEMENT ============

    async createSession(sessionId, mountpointId) {
        console.log(`[${this.nodeId}] Creating session ${sessionId}...`);

        // Validazione
        if (!sessionId || !mountpointId) {
            throw new Error('Missing fields: sessionId, mountpointId');
        }

        if (this.currentSession.active) {
            throw new Error(`Session already active: ${this.currentSession.sessionId}`);
        }

        try {
            this.whepServer.createEndpoint({
                id: String(sessionId),
                mountpoint: mountpointId,
                token: this.whepToken
            });
        } catch (error) {
            console.error(`[${this.nodeId}] Failed to create WHEP endpoint:`, error.message);
            throw error;
        }

        // Salva stato
        this.currentSession = {
            sessionId,
            mountpointId,
            active: true,
            createdAt: Date.now()
        };

        console.log(`[${this.nodeId}] Session ${sessionId} created`);
        console.log(`[${this.nodeId}] Mountpoint ID: ${mountpointId}`);
        console.log(`[${this.nodeId}] WHEP URL: http://${this.host}:${this.port}${this.whepBasePath}/endpoint/${sessionId}`);

        return this.currentSession;
    }

    async destroySession() {
        if (!this.currentSession.active) {
            throw new Error('No active session');
        }

        console.log(`[${this.nodeId}] Destroying session ${this.currentSession.sessionId}...`);

        const { sessionId } = this.currentSession;

        try {
            this.whepServer.destroyEndpoint(String(sessionId));
            console.log(`[${this.nodeId}] WHEP endpoint destroyed`);

        } catch (error) {
            console.error(`[${this.nodeId}] Error destroying WHEP endpoint:`, error.message);
            throw error;
        } finally {
            // Reset stato
            this.currentSession = {
                sessionId: null,
                mountpointId: null,
                active: false,
                createdAt: null
            };

            console.log(`[${this.nodeId}] Session destroyed`);
        }

        return { sessionId };
    }

    // ============ RTP FORWARDING (GSTREAMER) ============

    _startRtpForwarding() {
        console.log(`[${this.nodeId}] Starting RTP forwarding to ${this.rtpDestination.host}`);
        this._startAudioPipeline();
        this._startVideoPipeline();
    }

    _startAudioPipeline() {
        const args = [
            'udpsrc', `port=${this.rtp.audioPort}`,
            'caps=application/x-rtp,media=audio,encoding-name=OPUS,clock-rate=48000,payload=111',
            '!', 'udpsink',
            `host=${this.rtpDestination.host}`,
            `port=${this.rtpDestination.audioPort}`,
            'sync=false',
            'async=false'
        ];

        const attempt = this.pipelineHealth.audio.restarts + 1;
        console.log(`[${this.nodeId}] Starting audio pipeline (attempt #${attempt})`);
        console.log(`[${this.nodeId}] Audio pipeline: gst-launch-1.0 ${args.join(' ')}`);


        this.audioPipeline = spawn('gst-launch-1.0', args);

        // Update health
        this.pipelineHealth.audio.running = true;
        this.pipelineHealth.audio.restarts++;
        this.pipelineHealth.audio.lastError = null;

        // Attach handlers
        this._attachPipelineHandlers('audio', this.audioPipeline, () => {
            this._startAudioPipeline();  // Callback per restart
        });
    }

    _startVideoPipeline() {
        const args = [
            'udpsrc', `port=${this.rtp.videoPort}`,
            'caps=application/x-rtp,media=video,encoding-name=VP8,clock-rate=90000,payload=96',
            '!', 'udpsink',
            `host=${this.rtpDestination.host}`,
            `port=${this.rtpDestination.videoPort}`,
            'sync=false',
            'async=false'
        ];

        const attempt = this.pipelineHealth.video.restarts + 1;
        console.log(`[${this.nodeId}] Starting video pipeline (attempt #${attempt})`);
        console.log(`[${this.nodeId}] Video pipeline: gst-launch-1.0 ${args.join(' ')}`);


        this.videoPipeline = spawn('gst-launch-1.0', args);

        // Update health
        this.pipelineHealth.video.running = true;
        this.pipelineHealth.video.restarts++;
        this.pipelineHealth.video.lastError = null;

        // Attach handlers
        this._attachPipelineHandlers('video', this.videoPipeline, () => {
            this._startVideoPipeline();  // Callback per restart
        });
    }

    // ============ VIEWER COUNT HELPER ============

    _getViewerCount(sessionId) {
        try {
            const endpoints = this.whepServer.listEndpoints();
            const currentEndpoint = endpoints.find(ep => ep.id === sessionId);

            return currentEndpoint?.subscribers || 0;
        } catch (error) {
            console.error(`[${this.nodeId}] Error getting viewer count:`, error.message);
            return 0;
        }
    }

    // ============ API ============

    setupEgressAPI() {
        // POST /session/create
        this.app.post('/session/create', async (req, res) => {
            try {
                const { sessionId, mountpointId } = req.body;
                const result = await this.createSession(sessionId, mountpointId);

                res.status(201).json({
                    sessionId: result.sessionId,
                    mountpointId: result.mountpointId,
                    whepUrl: `http://${this.host}:${this.port}${this.whepBasePath}/endpoint/${sessionId}`,
                    createdAt: result.createdAt
                });

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
                const viewers = this._getViewerCount(this.currentSession.sessionId);

                res.json({
                    active: true,
                    sessionId: this.currentSession.sessionId,
                    mountpointId: this.currentSession.mountpointId,
                    viewers: viewers,
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

        // GET /pipelines - Pipeline health status
        this.app.get('/pipelines', (req, res) => {
            res.json({
                destination: this.rtpDestination,
                audio: {
                    running: this.pipelineHealth.audio.running,
                    restarts: this.pipelineHealth.audio.restarts,
                    lastError: this.pipelineHealth.audio.lastError,
                    port: this.rtp.audioPort,
                    forwardTo: `${this.rtpDestination.host}:${this.rtpDestination.audioPort}`
                },
                video: {
                    running: this.pipelineHealth.video.running,
                    restarts: this.pipelineHealth.video.restarts,
                    lastError: this.pipelineHealth.video.lastError,
                    port: this.rtp.videoPort,
                    forwardTo: `${this.rtpDestination.host}:${this.rtpDestination.videoPort}`
                }
            });
        });
    }

    async getStatus() {
        const baseStatus = await super.getStatus();

        return {
            ...baseStatus,
            janus: {
                connected: this.whepServer !== null,
                url: this.janusUrl
            },
            whep: {
                running: this.whepServer !== null,
                basePath: this.whepBasePath
            },
            pipelines: {
                audio: {
                    running: this.pipelineHealth.audio.running,
                    restarts: this.pipelineHealth.audio.restarts
                },
                video: {
                    running: this.pipelineHealth.video.running,
                    restarts: this.pipelineHealth.video.restarts
                }
            },
            session: this.currentSession.active ? {
                active: true,
                sessionId: this.currentSession.sessionId,
                mountpointId: this.currentSession.mountpointId,
                viewers: this._getViewerCount(this.currentSession.sessionId),
                createdAt: this.currentSession.createdAt,
                uptime: Math.floor((Date.now() - this.currentSession.createdAt) / 1000)
            } : {
                active: false,
                sessionId: null
            }
        };
    }
}