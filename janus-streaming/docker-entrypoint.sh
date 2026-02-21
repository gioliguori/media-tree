#!/bin/bash
set -e

export JANUS_RTP_PORT_RANGE="${JANUS_RTP_PORT_RANGE:-20000-20099}"
export JANUS_STREAMING_RTP_PORT_RANGE="${JANUS_STREAMING_RTP_PORT_RANGE:-6000-6200}"
export JANUS_NAT_1_1_MAPPING="${JANUS_NAT_1_1_MAPPING:-127.0.0.1}"

export JANUS_LOG_LEVEL="${JANUS_LOG_LEVEL:-4}"

echo "[Janus Streaming] RTP Port Range: $JANUS_RTP_PORT_RANGE"
echo "[Janus] Streaming Input Range: $JANUS_STREAMING_RTP_PORT_RANGE"
echo "[Janus Streaming] Log Level: $JANUS_LOG_LEVEL"
echo "[Janus] NAT Mapping: $JANUS_NAT_1_1_MAPPING"

envsubst < /opt/janus/etc/janus/janus.jcfg.template > /opt/janus/etc/janus/janus.jcfg

envsubst < /opt/janus/etc/janus/janus.plugin.streaming.jcfg.template > /opt/janus/etc/janus/janus.plugin.streaming.jcfg

echo "[Janus Streaming] Config generated successfully"

exec "$@"