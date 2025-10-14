import { EgressNode } from './EgressNode.js';

const config = {
    nodeId: process.env.NODE_ID || 'egress-1',
    nodeType: 'egress',

    // Network
    host: process.env.NODE_HOST || 'egress-1',
    port: parseInt(process.env.API_PORT) || 7071,

    rtp: {
        audioPort: parseInt(process.env.RTP_AUDIO_PORT) || 5002,
        videoPort: parseInt(process.env.RTP_VIDEO_PORT) || 5004
    },

    // Redis
    redis: {
        host: process.env.REDIS_HOST || 'redis',
        port: parseInt(process.env.REDIS_PORT) || 6379,
        password: process.env.REDIS_PASSWORD
    },

    // Janus Streaming
    janus: {
        streaming: {
            wsUrl: process.env.JANUS_STREAMING_WS_URL || 'ws://janus-streaming:8188'
        }
    },

    // WHEP Server
    whep: {
        basePath: process.env.WHEP_BASE_PATH || '/whep',
        token: process.env.WHEP_TOKEN || 'verysecret'
    }
};

// ============ AVVIO ============

const node = new EgressNode(config);

async function start() {
    console.log('Starting Egress Node...');
    console.log('Config:', JSON.stringify(config, null, 2));

    try {
        // Inizializza
        await node.initialize();

        // Setup API egress-specific
        node.setupEgressAPI();

        // Avvia
        await node.start();

        console.log('Egress Node running');
        console.log(`Node ID: ${config.nodeId}`);
        console.log(`API: http://${config.host}:${config.port}`);
        console.log(`WHEP: http://${config.host}:${config.port}${config.whep.basePath}`);
        console.log(`RTP Ports: ${config.rtp.audioPort}/${config.rtp.videoPort}`);
        console.log(`Janus: ${config.janus.streaming.wsUrl}`);

    } catch (error) {
        console.error('Failed to start Egress Node:', error);
        process.exit(1);
    }
}

// docker
process.on('SIGTERM', async () => {
    console.log('SIGTERM received, shutting down...');
    await node.stop();
    process.exit(0);
});

process.on('SIGINT', async () => {
    console.log('SIGINT received, shutting down...');
    await node.stop();
    process.exit(0);
});

start();