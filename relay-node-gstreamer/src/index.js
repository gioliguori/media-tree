import { GStreamerRelayNode } from './GStreamerRelayNode.js';

const config = {
    nodeId: process.env.NODE_ID || 'relay-gst-1',
    nodeType: 'relay',
    host: process.env.NODE_HOST || 'relay-gst-1',
    port: parseInt(process.env.API_PORT) || 7070,

    rtp: {
        audioPort: parseInt(process.env.AUDIO_PORT) || 5002,
        videoPort: parseInt(process.env.VIDEO_PORT) || 5004
    },

    redis: {
        host: process.env.REDIS_HOST || 'redis',
        port: parseInt(process.env.REDIS_PORT) || 6379,
        password: process.env.REDIS_PASSWORD
    }
};

const node = new GStreamerRelayNode(config);

async function start() {
    try {
        await node.initialize();
        await node.start();
        console.log(`GStreamerRelayNode ${config.nodeId} running`);
    } catch (error) {
        console.error('Fatal error:', error);
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