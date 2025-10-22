export async function saveSessionToRedis(redis, nodeId, sessionData) {
    const { sessionId, roomId, audioSsrc, videoSsrc, recipients, createdAt } = sessionData;

    await redis.hset(`session:${sessionId}`, {
        sessionId,
        roomId: String(roomId),
        audioSsrc: String(audioSsrc),
        videoSsrc: String(videoSsrc),
        recipients: JSON.stringify(recipients),
        injectionNodeId: nodeId,
        active: 'true',
        createdAt: String(createdAt),
        updatedAt: String(createdAt)
    });

    await redis.sadd(`sessions:${nodeId}`, sessionId);
    await redis.sadd('sessions:active', sessionId);
}

export async function deactivateSessionInRedis(redis, nodeId, sessionId) {
    await redis.hset(`session:${sessionId}`, 'active', 'false');
    await redis.hset(`session:${sessionId}`, 'updatedAt', String(Date.now()));

    await redis.srem(`sessions:${nodeId}`, sessionId);
    await redis.srem('sessions:active', sessionId);

    // TTL 24 ore
    await redis.expire(`session:${sessionId}`, 86400);
    // console.log(`Session ${sessionId} will expire in 24h`);

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
            active: session.active,
            createdAt: session.createdAt,
            uptime: Math.floor((Date.now() - session.createdAt) / 1000)
        });
    }
    return sessions;
}