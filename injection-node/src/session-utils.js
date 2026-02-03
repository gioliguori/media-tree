export async function saveSessionToRedis(redis, nodeId, sessionData) {
    const { sessionId, roomId, audioSsrc, videoSsrc, recipients, endpoint, createdAt } = sessionData;
    // ci pensa il controller
    // const whipEndpointUrl = typeof endpoint === 'string'
    //     ? endpoint
    //     : (endpoint?.url || endpoint?.path || '');

    // await redis.hset(`tree:${treeId}:session:${sessionId}`, {
    //     sessionId,
    //     treeId,
    //     roomId: String(roomId),
    //     audioSsrc: String(audioSsrc),
    //     videoSsrc: String(videoSsrc),
    //     recipients: JSON.stringify(recipients),
    //     injectionNodeId: nodeId,
    //     //whipEndpoint: endpoint,
    //     active: 'true',
    //     createdAt: String(createdAt),
    //     updatedAt: String(createdAt)
    // });

    // await redis.sadd(`tree:${treeId}:sessions`, sessionId);

    // await redis.sadd(`tree:${treeId}:sessions:node:${nodeId}`, sessionId);
}

export async function deactivateSessionInRedis(redis, nodeId, sessionId) {
    // ci pensa il controller
    // Rimuovi completamente l'hash
    // await redis.del(`tree:${treeId}:session:${sessionId}`);

    // // Rimuovi dai SET di indice
    // await redis.srem(`tree:${treeId}:sessions`, sessionId);
    // await redis.srem(`tree:${treeId}:sessions:node:${nodeId}`, sessionId);

}

export function getSessionInfo(sessionsMap, sessionId) {
    const session = sessionsMap.get(sessionId);
    if (!session) return null;

    return {
        sessionId: session.sessionId,
        roomId: session.roomId,
        audioSsrc: session.audioSsrc,
        videoSsrc: session.videoSsrc,
        recipients: session.recipients,
        whipEndpoint: `/whip/endpoint/${sessionId}`,
        active: session.active,
        createdAt: session.createdAt,
        uptime: Math.floor((Date.now() - session.createdAt) / 1000)
    };
}

export function getAllSessionsInfo(sessionsMap) {
    const sessions = [];
    for (const [sessionId, session] of sessionsMap.entries()) {
        sessions.push({
            sessionId: session.sessionId,
            roomId: session.roomId,
            audioSsrc: session.audioSsrc,
            videoSsrc: session.videoSsrc,
            whipEndpoint: `/whip/endpoint/${sessionId}`,
            active: session.active,
            createdAt: session.createdAt,
            uptime: Math.floor((Date.now() - session.createdAt) / 1000)
        });
    }
    return sessions;
}