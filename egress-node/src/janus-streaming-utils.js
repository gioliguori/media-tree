import Janode from 'janode';
import StreamingPlugin from 'janode/plugins/streaming';

export async function connectToJanusStreaming(nodeId, janusConfig) {
    try {
        console.log(`[${nodeId}] Connecting to Janus at ${janusConfig.wsUrl}...`);

        const connectionConfig = {
            address: [{ url: janusConfig.wsUrl }],
            retry_time_secs: 5
        };

        // api secret in janus.jcfg
        if (janusConfig.apiSecret) {
            connectionConfig.address[0].apisecret = janusConfig.apiSecret;
            // console.log(`[${nodeId}] Using Janus API secret for authentication`);
        }

        const connection = await Janode.connect(connectionConfig);

        // Eventi connessione
        connection.once(Janode.EVENT.CONNECTION_CLOSED, () => {
            console.log(`[${nodeId}] Janus connection closed`);
        });

        connection.once(Janode.EVENT.CONNECTION_ERROR, error => {
            console.error(`[${nodeId}] Janus connection error: ${error.message}`);
        });

        // Crea sessione
        const session = await connection.create();
        console.log(`[${nodeId}] Janus session ${session.id} created`);

        // Eventi sessione
        session.once(Janode.EVENT.SESSION_DESTROYED, () => {
            console.log(`[${nodeId}] Janus session ${session.id} destroyed`);
        });

        // Attach videoRoom plugin
        const streaming = await session.attach(StreamingPlugin);
        console.log(`[${nodeId}] Streaming manager handle ${streaming.id} attached`);

        streaming.once(Janode.EVENT.HANDLE_DETACHED, () => {
            console.log(`[${nodeId}] Streaming manager handle detached`);
        });

        console.log(`[${nodeId}] Connected to Janus Streaming plugin`);

        return { connection, session, streaming };

    } catch (error) {
        console.error(`[${nodeId}] Failed to connect to Janus:`, error.message);
        throw error;
    }
}

// nodeId lo passiamo solo per loggare non so se ha senso
export async function createJanusMountpoint(streaming, nodeId, mountpointId, audioPort, videoPort, secret) {
    try {
        console.log(`[Janus:${nodeId}] Creating mountpoint ${mountpointId}...`);
        console.log(`[Janus:${nodeId}] Port=${audioPort}`);
        console.log(`[Janus:${nodeId}] Port=${videoPort}`);

        // molti parametri sono harcodati per il momento
        const response = await streaming.createRtpMountpoint({
            id: mountpointId,
            name: `Mountpoint ${mountpointId}`,
            description: `Session mountpoint ${mountpointId}`,
            is_private: false,
            permanent: false,
            secret: secret,
            media: [
                {
                    type: 'audio',
                    mid: '0',
                    port: audioPort,
                    pt: 111,
                    codec: 'opus',
                    rtpmap: 'opus/48000/2'
                },
                {
                    type: 'video',
                    mid: '1',
                    port: videoPort,
                    pt: 96,
                    codec: 'h264',
                    rtpmap: 'h264/90000'
                }
            ]
        });

        console.log(`[Janus:${nodeId}]  Mountpoint ${mountpointId} created successfully`);
        return response;
    } catch (error) {
        console.error(`[Janus:${nodeId}]  Failed to create mountpoint ${mountpointId}:`, error.message);
        throw error;
    }
}

export async function destroyJanusMountpoint(streaming, mountpointId, secret) {
    try {
        console.log(`[Janus] Destroying mountpoint ${mountpointId}...`);

        await streaming.destroyMountpoint({
            id: mountpointId,
            secret: secret,
            permanent: false
        });

        console.log(`[Janus] Mountpoint ${mountpointId} destroyed`);
    } catch (error) {
        console.error(`[Janus] Error destroying mountpoint ${mountpointId}:`, error.message);
        throw error;
    }
}