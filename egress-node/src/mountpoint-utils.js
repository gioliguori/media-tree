export async function saveMountpointToRedis(redis, nodeId, mountpointData) {
    const { sessionId, mountpointId, audioSsrc, videoSsrc, janusAudioPort, janusVideoPort, createdAt } = mountpointData;

    // hset -> hash set, di redis
    await redis.hset(`mountpoint:${nodeId}:${sessionId}`, {
        sessionId,
        mountpointId: String(mountpointId),
        audioSsrc: String(audioSsrc),
        videoSsrc: String(videoSsrc),
        janusAudioPort: String(janusAudioPort),
        janusVideoPort: String(janusVideoPort),
        egressNodeId: nodeId,
        active: 'true',
        createdAt: String(createdAt),
        updatedAt: String(Date.now())
    });

    // Indice dei mountpoint per questo specifico nodo
    await redis.sadd(`node:${nodeId}:mountpoints`, sessionId);
    // Indice globale
    await redis.sadd(`mountpoints:global`, `${nodeId}:${sessionId}`);
}

export async function deactivateMountpointInRedis(redis, nodeId, sessionId) {
    await redis.del(`mountpoint:${nodeId}:${sessionId}`);
    await redis.srem(`node:${nodeId}:mountpoints`, sessionId);
    await redis.srem(`mountpoints:global`, `${nodeId}:${sessionId}`);
}

export function getMountpointInfo(mountpointsMap, sessionId) {
    const mountpoint = mountpointsMap.get(sessionId);
    if (!mountpoint) return null;

    return {
        sessionId: mountpoint.sessionId,
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