export async function saveSessionToRedis(redis, treeId, nodeId, sessionData) {
    const { sessionId, roomId, audioSsrc, videoSsrc, recipients, createdAt } = sessionData;

    await redis.hset(`tree:${treeId}:session:${sessionId}`, {
        sessionId,
        treeId,
        roomId: String(roomId),
        audioSsrc: String(audioSsrc),
        videoSsrc: String(videoSsrc),
        recipients: JSON.stringify(recipients),
        injectionNodeId: nodeId,
        active: 'true',
        createdAt: String(createdAt),
        updatedAt: String(createdAt)
    });

    await redis.sadd(`sessions:${treeId}`, sessionId);
}

export async function deactivateSessionInRedis(redis, treeId, sessionId) {
    await redis.hset(`tree:${treeId}:session:${sessionId}`, 'active', 'false');
    await redis.hset(`tree:${treeId}:session:${sessionId}`, 'updatedAt', String(Date.now()));

    await redis.srem(`sessions:${treeId}`, sessionId);

    // TTL 24 ore
    await redis.expire(`tree:${treeId}:session:${sessionId}`, 86400);

}

export function getSessionInfo(sessionsMap, sessionId) {
    const session = sessionsMap.get(sessionId);
    if (!session) return null;

    return {
        sessionId: session.sessionId,
        treeId: session.treeId,
        roomId: session.roomId,
        audioSsrc: session.audioSsrc,
        videoSsrc: session.videoSsrc,
        recipients: session.recipients,
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
            treeId: session.treeId,
            roomId: session.roomId,
            audioSsrc: session.audioSsrc,
            videoSsrc: session.videoSsrc,
            active: session.active,
            createdAt: session.createdAt,
            uptime: Math.floor((Date.now() - session.createdAt) / 1000)
        });
    }
    return sessions;
}