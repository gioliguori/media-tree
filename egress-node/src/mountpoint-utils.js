export async function saveMountpointToRedis(redis, treeId, nodeId, mountpointData) {
    const { sessionId, mountpointId, audioSsrc, videoSsrc, janusAudioPort, janusVideoPort, createdAt } = mountpointData;

    // hset -> hash set, di redis
    await redis.hset(`tree:${treeId}:mountpoint:${nodeId}:${sessionId}`, {
        sessionId,
        treeId,
        mountpointId: String(mountpointId),
        audioSsrc: String(audioSsrc),
        videoSsrc: String(videoSsrc),
        janusAudioPort: String(janusAudioPort),
        janusVideoPort: String(janusVideoPort),
        egressNodeId: nodeId,
        active: 'true',
        createdAt: String(createdAt),
        updatedAt: String(createdAt)
    });

    await redis.sadd(`mountpoints:${treeId}`, `${nodeId}:${sessionId}`);
    await redis.sadd(`tree:${treeId}:mountpoints:node:${nodeId}`, sessionId);
}

export async function deactivateMountpointInRedis(redis, treeId, nodeId, sessionId) {

    await redis.hset(`tree:${treeId}:mountpoint:${nodeId}:${sessionId}`, 'active', 'false');
    await redis.hset(`tree:${treeId}:mountpoint:${nodeId}:${sessionId}`, 'updatedAt', String(Date.now()));

    await redis.srem(`mountpoints:${treeId}`, `${nodeId}:${sessionId}`);
    await redis.srem(`tree:${treeId}:mountpoints:node:${nodeId}`, sessionId);

    // TTL 24 ore
    await redis.expire(`tree:${treeId}:mountpoint:${nodeId}:${sessionId}`, 86400);
}

export function getMountpointInfo(mountpointsMap, sessionId) {
    const mountpoint = mountpointsMap.get(sessionId);
    if (!mountpoint) return null;

    return {
        sessionId: mountpoint.sessionId,
        treeId: mountpoint.treeId,
        mountpointId: mountpoint.mountpointId,
        audioSsrc: mountpoint.audioSsrc,
        videoSsrc: mountpoint.videoSsrc,
        janusAudioPort: mountpoint.janusAudioPort,
        janusVideoPort: mountpoint.janusVideoPort,
        active: mountpoint.active,
        createdAt: mountpoint.createdAt,
        uptime: Math.floor((Date.now() - mountpoint.createdAt) / 1000)
    };
}

export function getAllMountpointsInfo(mountpointsMap) {
    const mountpoints = [];

    for (const [sessionId, mountpoint] of mountpointsMap.entries()) {
        mountpoints.push({
            sessionId: mountpoint.sessionId,
            treeId: mountpoint.treeId,
            mountpointId: mountpoint.mountpointId,
            audioSsrc: mountpoint.audioSsrc,
            videoSsrc: mountpoint.videoSsrc,
            janusAudioPort: mountpoint.janusAudioPort,
            janusVideoPort: mountpoint.janusVideoPort,
            active: mountpoint.active,
            createdAt: mountpoint.createdAt,
            uptime: Math.floor((Date.now() - mountpoint.createdAt) / 1000)
        });
    }

    return mountpoints;
}