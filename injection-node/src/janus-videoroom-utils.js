import Janode from 'janode';
import VideoRoomPlugin from 'janode/plugins/videoroom';

export async function connectToJanusVideoroom(nodeId, janusConfig) {
    try {
        console.log(`[${nodeId}] Connecting to Janus at ${janusConfig.wsUrl}...`);

        const connectionConfig = {
            address: [{ url: janusConfig.wsUrl }],
            retry_time_secs: 5
        };

        // api secret in janus.jcfg
        if (janusConfig.apiSecret) {
            connectionConfig.address[0].apisecret = janusConfig.apiSecret;
            console.log(`[${nodeId}] Using Janus API secret for authentication`);
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
        const videoRoom = await session.attach(VideoRoomPlugin);
        console.log(`[${nodeId}] VideoRoom manager handle ${videoRoom.id} attached`);

        videoRoom.once(Janode.EVENT.HANDLE_DETACHED, () => {
            console.log(`[${nodeId}] VideoRoom manager handle detached`);
        });

        console.log(`[${nodeId}] Connected to Janus VideoRoom plugin`);

        return { connection, session, videoRoom };

    } catch (error) {
        console.error(`[${nodeId}] Failed to connect to Janus:`, error.message);
        throw error;
    }
}

// nodeId lo passiamo solo per loggare non so se ha senso
export async function createJanusRoom(videoRoom, nodeId, roomId, description, secret) {
    try {
        console.log(`[Janus:${nodeId}] Creating room ${roomId}...`);

        // alcuni parametri hardcodati (va bene cosi per il momento)
        const response = await videoRoom.create({
            room: roomId,
            description: description,
            secret: secret,
            publishers: 1,
            bitrate: 2048000,
            fir_freq: 10,
            permanent: false,
            record: false
            // audiocodec e videocodec: usa default Janus (opus/vp8)
        });

        console.log(`[Janus:${nodeId}]  Room ${roomId} created successfully`);
        return response;
    } catch (error) {
        console.error(`[Janus:${nodeId}] Failed to create room ${roomId}:`, error.message);
        throw error;
    }
}

export async function destroyJanusRoom(videoRoom, nodeId, roomId, secret) {
    try {
        console.log(`[Janus:${nodeId}]  Destroying room ${roomId}...`);

        await videoRoom.destroy({
            room: roomId,
            permanent: false,
            secret: secret
        });

        console.log(`[Janus:${nodeId}]  Room ${roomId} destroyed`);
    } catch (error) {
        console.error(`[Janus:${nodeId}]  Error destroying room ${roomId}:`, error.message);
        throw error;
    }
}