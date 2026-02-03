import { InjectionNode } from './InjectionNode.js';

const config = {
    nodeId: process.env.NODE_ID || 'injection-1',
    nodeType: 'injection',
    host: process.env.NODE_HOST || 'injection-1',
    port: parseInt(process.env.API_PORT) || 7070,

    // RTP Ports solo per registrazione Redis
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

    // Janus VideoRoom
    janus: {
        videoroom: {
            wsUrl: process.env.JANUS_VIDEOROOM_WS_URL || 'ws://janus-videoroom:8188',
            apiSecret: process.env.JANUS_VIDEOROOM_API_SECRET || null,
            roomSecret: process.env.JANUS_VIDEOROOM_ROOM_SECRET || 'adminpwd'
        }
    },

    // WHIP Server
    whip: {
        basePath: process.env.WHIP_BASE_PATH || '/whip',
        token: process.env.WHIP_TOKEN || 'verysecret',
    }
};

// AVVIO

const node = new InjectionNode(config);
async function start() {
    console.log(' Starting Injection Node...');
    console.log('Config:', JSON.stringify(config, null, 2));

    try {
        // Inizializza
        await node.initialize();

        // Setup API
        node.setupInjectionAPI();

        // Avvia
        await node.start();

        console.log('   Injection Node running');
        console.log(`   Node ID: ${config.nodeId}`);
    } catch (error) {
        console.error(`[${config.nodeId}] Failed to start:`, error);
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