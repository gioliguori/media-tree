export async function saveMountpointToRedis(redis, nodeId, mountpointData) {
    const { sessionId, mountpointId, audioSsrc, videoSsrc, janusAudioPort, janusVideoPort, createdAt } = mountpointData;

    // hset -> hash set, di redis
    await redis.hset(`mountpoint:${nodeId}:${mountpointId}`, {
        sessionId,
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

    await redis.sadd(`mountpoints:${nodeId}`, String(mountpointId));
    await redis.sadd('mountpoints:active', `${nodeId}:${mountpointId}`);
}

export async function deactivateMountpointInRedis(redis, nodeId, mountpointId) {

    await redis.hset(`mountpoint:${nodeId}:${mountpointId}`, 'active', 'false');
    await redis.hset(`mountpoint:${nodeId}:${mountpointId}`, 'updatedAt', String(Date.now()));

    await redis.srem(`mountpoints:${nodeId}`, String(mountpointId));
    await redis.srem('mountpoints:active', `${nodeId}:${mountpointId}`);

    // TTL 24 ore
    await redis.expire(`mountpoint:${nodeId}:${mountpointId}`, 86400);
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
            active: mountpoint.active,
            createdAt: mountpoint.createdAt,
            uptime: Math.floor((Date.now() - mountpoint.createdAt) / 1000)
        });
    }

    return mountpoints;
}