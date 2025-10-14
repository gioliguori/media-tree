#!/bin/bash

MODE=${MODE:-sender}
TARGET_HOST=${TARGET_HOST:-relay-1}
AUDIO_PORT=${AUDIO_PORT:-5002}
VIDEO_PORT=${VIDEO_PORT:-5004}
LISTEN_AUDIO_PORT=${LISTEN_AUDIO_PORT:-6000}
LISTEN_VIDEO_PORT=${LISTEN_VIDEO_PORT:-6002}

echo "=========================================="
echo "Mode: $MODE"

if [ "$MODE" = "sender" ]; then
    echo "Target: $TARGET_HOST"
    echo "Audio → $TARGET_HOST:$AUDIO_PORT (Opus)"
    echo "Video → $TARGET_HOST:$VIDEO_PORT (VP8)"
    echo "=========================================="
    echo ""
    
    gst-launch-1.0 \
        audiotestsrc is-live=true wave=sine freq=440 ! \
        audioconvert ! audioresample ! \
        opusenc ! rtpopuspay pt=111 ! \
        udpsink host=$TARGET_HOST port=$AUDIO_PORT \
        \
        videotestsrc is-live=true pattern=ball ! \
        video/x-raw,width=640,height=480,framerate=30/1 ! \
        videoconvert ! \
        vp8enc deadline=1 target-bitrate=500000 ! \
        rtpvp8pay pt=96 ! \
        udpsink host=$TARGET_HOST port=$VIDEO_PORT

elif [ "$MODE" = "receiver" ]; then
    echo "Audio ← 0.0.0.0:$LISTEN_AUDIO_PORT (Opus)"
    echo "Video ← 0.0.0.0:$LISTEN_VIDEO_PORT (VP8)"
    echo "=========================================="
    echo ""
    
    gst-launch-1.0 \
        udpsrc port=$LISTEN_AUDIO_PORT caps="application/x-rtp,media=audio,encoding-name=OPUS,clock-rate=48000,payload=111" ! \
        rtpopusdepay ! opusdec ! \
        audioconvert ! audioresample ! \
        fakesink sync=false \
        \
        udpsrc port=$LISTEN_VIDEO_PORT caps="application/x-rtp,media=video,encoding-name=VP8,clock-rate=90000,payload=96" ! \
        rtpvp8depay ! vp8dec ! \
        videoconvert ! \
        fakesink sync=false
fi