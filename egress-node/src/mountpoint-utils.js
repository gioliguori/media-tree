export async function saveMountpointToRedis(redis, treeId, nodeId, mountpointData) {
    const { sessionId, mountpointId, audioSsrc, videoSsrc, janusAudioPort, janusVideoPort, endpoint, createdAt } = mountpointData;

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
        //whepEndpoint: endpoint,
        active: 'true',
        createdAt: String(createdAt),
        updatedAt: String(createdAt)
    });

    await redis.sadd(`tree:${treeId}:mountpoints`, `${nodeId}:${sessionId}`);
    await redis.sadd(`tree:${treeId}:mountpoints:node:${nodeId}`, sessionId);
}

export async function deactivateMountpointInRedis(redis, treeId, nodeId, sessionId) {
    // Rimuovi completamente l'hash
    await redis.del(`tree:${treeId}:mountpoint:${nodeId}:${sessionId}`);

    // Rimuovi dai SET di indice
    await redis.srem(`tree:${treeId}:mountpoints`, `${nodeId}:${sessionId}`);
    await redis.srem(`tree:${treeId}:mountpoints:node:${nodeId}`, sessionId);
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
        whepEndpoint: `/whep/endpoint/${sessionId}`,
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
            whepEndpoint: `/whep/endpoint/${sessionId}`,
            active: mountpoint.active,
            createdAt: mountpoint.createdAt,
            uptime: Math.floor((Date.now() - mountpoint.createdAt) / 1000)
        });
    }

    return mountpoints;
}