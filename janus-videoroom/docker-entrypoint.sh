#!/bin/bash
set -e

# Default values
export JANUS_RTP_PORT_RANGE="${JANUS_RTP_PORT_RANGE:-20000-20099}"
export JANUS_LOG_LEVEL="${JANUS_LOG_LEVEL:-4}"

echo "[Janus Streaming] RTP Port Range: $JANUS_RTP_PORT_RANGE"
echo "[Janus Streaming] Log Level: $JANUS_LOG_LEVEL"

# Generate config from template
envsubst < /opt/janus/etc/janus/janus.jcfg.template > /opt/janus/etc/janus/janus.jcfg

echo "[Janus Streaming] Config generated successfully"

# Execute command
exec "$@"