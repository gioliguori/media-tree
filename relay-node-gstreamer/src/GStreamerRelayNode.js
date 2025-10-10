import { BaseNode } from '../shared/BaseNode.js';
import { spawn } from 'child_process';

export class GStreamerRelayNode extends BaseNode {
    constructor(config) {
        super(config.nodeId, 'relay', config);

        // GStreamer processes
        this.audioProcess = null;
        this.videoProcess = null;


        // cache qui non serve perchè il forwarding lo fa gstreamer, nell'altro lo facevamo noi a mano
        // this.childrenTargets = [];

        // Stats
        this.stats = {
            audioRestarts: 0,
            videoRestarts: 0,
            lastRestart: null
        };
    }

    async onInitialize() {
        console.log(`[${this.nodeId}] GStreamer relay node initialized`);
        console.log(`[${this.nodeId}] Listening for RTP on ${this.rtp.audioPort}/${this.rtp.videoPort}`);
        // Controlla se ci sono già children registrati altrimenti facciamo polling...
        const childrenInfo = await this.getChildrenInfo();

        if (childrenInfo.length > 0) {
            console.log(`[${this.nodeId}] Found ${childrenInfo.length} children at startup, building pipelines...`);
            await this.rebuildPipelines();
        } else {
            console.log(`[${this.nodeId}] No children yet, waiting for polling...`);
        }
    }

    async onChildrenChanged(added, removed) {
        console.log(`[${this.nodeId}] Children changed (+${added.length} -${removed.length})`);

        if (added.length > 0) {
            console.log(`[${this.nodeId}] Added children:`, added);
        }
        if (removed.length > 0) {
            console.log(`[${this.nodeId}] Removed children:`, removed);
        }

        console.log(`[${this.nodeId}] Rebuilding GStreamer pipelines...`);
        await this.rebuildPipelines();
    }

    async rebuildPipelines() {

        this.stopPipelines();

        // Get children info da redis
        const childrenInfo = await this.getChildrenInfo();

        if (childrenInfo.length === 0) {
            console.log(`[${this.nodeId}] No children, pipelines stopped`);
            return;
        }

        console.log(`[${this.nodeId}] Starting pipelines for ${childrenInfo.length} children`);

        // Start new pipelines
        this.startAudioPipeline(childrenInfo);
        this.startVideoPipeline(childrenInfo);

        this.stats.lastRestart = new Date().toISOString();
    }

    startAudioPipeline(children) {
        const args = this.buildAudioPipelineArgs(children);

        console.log(`[${this.nodeId}] Audio pipeline: gst-launch-1.0 ${args.join(' ')}`);

        // facciamo partire il processo
        this.audioProcess = spawn('gst-launch-1.0', args);
        this.stats.audioRestarts++;

        // log
        this.audioProcess.stdout.on('data', (data) => {
            const msg = data.toString().trim();
            if (msg) console.log(`[${this.nodeId}] Audio stdout: ${msg}`);
        });

        this.audioProcess.stderr.on('data', (data) => {
            const msg = data.toString().trim();
            if (msg && !msg.includes('Setting pipeline to PAUSED') && !msg.includes('Setting pipeline to PLAYING')) {
                console.log(`[${this.nodeId}] Audio stderr: ${msg}`);
            }
        });

        // exit
        this.audioProcess.on('close', (code) => {
            console.log(`[${this.nodeId}] Audio pipeline exited (code ${code})`);
            this.audioProcess = null;
        });

        this.audioProcess.on('error', (err) => {
            console.error(`[${this.nodeId}] Audio pipeline error:`, err.message);
        });
    }

    startVideoPipeline(children) {
        const args = this.buildVideoPipelineArgs(children);

        console.log(`[${this.nodeId}] Video pipeline: gst-launch-1.0 ${args.join(' ')}`);

        // facciamo partire il processo
        this.videoProcess = spawn('gst-launch-1.0', args);
        this.stats.videoRestarts++;

        // log
        this.videoProcess.stdout.on('data', (data) => {
            const msg = data.toString().trim();
            if (msg) console.log(`[${this.nodeId}] Video stdout: ${msg}`);
        });

        this.videoProcess.stderr.on('data', (data) => {
            const msg = data.toString().trim();
            if (msg && !msg.includes('Setting pipeline to PAUSED') && !msg.includes('Setting pipeline to PLAYING')) {
                console.log(`[${this.nodeId}] Video stderr: ${msg}`);
            }
        });

        // exit
        this.videoProcess.on('close', (code) => {
            console.log(`[${this.nodeId}] Video pipeline exited (code ${code})`);
            this.videoProcess = null;
        });

        this.videoProcess.on('error', (err) => {
            console.error(`[${this.nodeId}] Video pipeline error:`, err.message);
        });
    }

    buildAudioPipelineArgs(children) {
        const args = [];

        // Source
        args.push('udpsrc');
        args.push(`port=${this.rtp.audioPort}`);
        args.push('caps=application/x-rtp,media=audio,encoding-name=OPUS,clock-rate=48000,payload=111');
        args.push('!');

        if (children.length === 1) {
            // single child
            args.push('udpsink');
            args.push(`host=${children[0].host}`);
            args.push(`port=${children[0].audioPort}`);
            args.push('sync=false');
            args.push('async=false');
        } else {
            // Multiple children (tee è uno splitter)
            args.push('tee');
            args.push('name=t_audio');
            args.push('allow-not-linked=true'); // ignora branch disconnessi altrimenti andrebbe in errore se uno solo crashasse

            children.forEach((child, index) => {
                args.push('t_audio.');
                args.push('!');
                args.push('queue');
                args.push('max-size-buffers=200');
                args.push('leaky=downstream'); // scarta se buffer pieno
                args.push('!');
                args.push('udpsink');
                args.push(`host=${child.host}`);
                args.push(`port=${child.audioPort}`);
                args.push('sync=false');
                args.push('async=false');
            });
        }

        return args;
    }

    buildVideoPipelineArgs(children) {
        const args = [];

        // Source
        args.push('udpsrc');
        args.push(`port=${this.rtp.videoPort}`);
        args.push('caps=application/x-rtp,media=video,encoding-name=VP8,clock-rate=90000,payload=96');
        args.push('!');

        if (children.length === 1) {
            // single child
            args.push('udpsink');
            args.push(`host=${children[0].host}`);
            args.push(`port=${children[0].videoPort}`);
            args.push('sync=false');
            args.push('async=false');
        } else {
            // Multiple children (tee è uno splitter)
            args.push('tee');
            args.push('name=t_video');
            args.push('allow-not-linked=true'); // ignora branch disconnessi altrimenti andrebbe in errore se uno solo crashasse

            children.forEach((child, index) => {
                args.push('t_video.');
                args.push('!');
                args.push('queue');
                args.push('max-size-buffers=200');
                args.push('leaky=downstream'); // scarta se buffer pieno
                args.push('!');
                args.push('udpsink');
                args.push(`host=${child.host}`);
                args.push(`port=${child.videoPort}`);
                args.push('sync=false');
                args.push('async=false');
            });
        }

        return args;
    }

    stopPipelines() {
        if (this.audioProcess) {
            console.log(`[${this.nodeId}] Stopping audio pipeline...`);
            this.audioProcess.kill('SIGTERM');
            this.audioProcess = null;
        }

        if (this.videoProcess) {
            console.log(`[${this.nodeId}] Stopping video pipeline...`);
            this.videoProcess.kill('SIGTERM');
            this.videoProcess = null;
        }
    }

    async onStop() {
        this.stopPipelines();
    }

    async getStatus() {
        const baseStatus = await super.getStatus();
        return {
            ...baseStatus,
            gstreamer: {
                audioRunning: this.audioProcess !== null,
                videoRunning: this.videoProcess !== null,
                stats: this.stats
            },
            forwarding: {
                childrenCount: this.children.length,
                children: this.children
            }
        };
    }
}