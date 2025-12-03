import { EgressNode } from './EgressNode.js';

const config = {
    nodeId: process.env.NODE_ID || 'egress-1',
    nodeType: 'egress',
    host: process.env.NODE_HOST || 'localhost',
    port: parseInt(process.env.API_PORT) || 7073,
    treeId: process.env.TREE_ID,
    layer: process.env.LAYER,

    // RTP config (BaseNode)
    rtp: {
        audioPort: parseInt(process.env.RTP_AUDIO_PORT) || 5002,
        videoPort: parseInt(process.env.RTP_VIDEO_PORT) || 5004
    },
    // port pool
    // portPoolBase: parseInt(process.env.PORT_POOL_BASE) || 6000,
    // portPoolSize: parseInt(process.env.PORT_POOL_SIZE) || 1000,

    // Redis config
    redis: {
        host: process.env.REDIS_HOST || 'localhost',
        port: parseInt(process.env.REDIS_PORT) || 6379
    },

    // Janus Streaming config
    janus: {
        streaming: {
            wsUrl: process.env.JANUS_STREAMING_WS_URL || 'ws://localhost:8188',
            apiSecret: process.env.JANUS_STREAMING_API_SECRET || null,
            mountpointSecret: process.env.JANUS_STREAMING_MOUNTPOINT_SECRET || 'adminpwd'
        }
    },

    // WHEP config
    whep: {
        basePath: process.env.WHEP_BASE_PATH || '/whep',
        token: process.env.WHEP_TOKEN || 'verysecret'
    }
};

const node = new EgressNode(config);

async function start() {
    try {
        await node.initialize();

        // Setup API
        node.setupEgressAPI();

        await node.start();

        console.log('   Egress Node running');
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