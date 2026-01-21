#!/bin/bash

# Script per stress test

set -e

# Configurazione
NUM_SESSIONS=${1:-10}
CONTROLLER_URL="http://localhost:8080"
NETWORK="media-tree"
WHIP_CLIENT_IMAGE_PATH="../simple-whip-client"
SESSION_DELAY=${2:-12}
PROJECT_NAME="stress"

echo "=================================================="
echo "STRESS TEST - Creazione automatica sessioni"
echo "=================================================="
echo ""
echo "Sessioni da creare:  $NUM_SESSIONS"
echo "Delay tra sessioni: ${SESSION_DELAY}s"
echo "Progetto Docker: $PROJECT_NAME"
echo ""
read -p "Premi INVIO per iniziare..."

# Build dell'immagine whip-client
echo ""
echo "--- BUILD WHIP CLIENT IMAGE ---"
WHIP_CLIENT_IMAGE=$(docker build -q $WHIP_CLIENT_IMAGE_PATH)
echo "Immagine:  $WHIP_CLIENT_IMAGE"

echo ""
echo "--- CREAZIONE SESSIONI E WHIP CLIENTS ---"

for i in $(seq 1 $NUM_SESSIONS); do
    SESSION_ID="stress-test-$i"
    
    echo ""
    echo "[$i/$NUM_SESSIONS] $SESSION_ID"
    
    # Crea la sessione e estrai il whipEndpoint
    RESPONSE=$(curl -s -X POST $CONTROLLER_URL/api/sessions \
        -H "Content-Type:application/json" \
        -d "{\"sessionId\":\"$SESSION_ID\"}")
    
    WHIP_ENDPOINT=$(echo $RESPONSE | jq -r '.whipEndpoint')
    INJECTION_NODE=$(echo $RESPONSE | jq -r '.injectionNodeId')
    
    echo "  └─ Injection:  $INJECTION_NODE"
    echo "  └─ Endpoint: $WHIP_ENDPOINT"
    
    # Avvia il whip-client con label Docker Compose
    docker run -d \
        --name "whip-client-stress-$i" \
        --network $NETWORK \
        --label "com.docker.compose.project=$PROJECT_NAME" \
        --label "com.docker.compose.service=whip-client-stress" \
        --label "com.docker.compose.config-hash=stress-test" \
        --label "com.docker.compose.container-number=$i" \
        --label "com.docker.compose.oneoff=False" \
        --label "com.docker.compose.version=stress-test-script" \
        -e "URL=$WHIP_ENDPOINT" \
        -e "TOKEN=verysecret" \
        -e "AUDIO_PIPE=audiotestsrc is-live=true wave=sine freq=$((440 + i * 10)) ! audioconvert ! audioresample ! queue ! opusenc !  rtpopuspay pt=111 ssrc=$((10000 + i * 2)) ! queue ! application/x-rtp,media=audio,encoding-name=OPUS,payload=111" \
        -e "VIDEO_PIPE=videotestsrc is-live=true pattern=ball !  video/x-raw,width=640,height=480,framerate=30/1 ! videoconvert ! queue ! x264enc tune=zerolatency speed-preset=veryfast bitrate=2000 !  rtph264pay pt=96 ssrc=$((10001 + i * 2)) config-interval=1 ! queue ! application/x-rtp,media=video,encoding-name=H264,payload=96" \
        -e "STUN_SERVER=stun://stun.l.google.com:19302" \
        -e "GST_DEBUG=2" \
        --restart unless-stopped \
        $WHIP_CLIENT_IMAGE > /dev/null
    
    echo "  └─ Client avviato"
    
    if [ $i -lt $NUM_SESSIONS ]; then
        echo "  └─ Attendo ${SESSION_DELAY}s prima della prossima sessione..."
        sleep $SESSION_DELAY
    fi
done

echo ""
echo "=================================================="
echo "COMPLETATO: $NUM_SESSIONS sessioni attive"
echo "=================================================="
echo ""
echo "Distribuzione per injection node:"
curl -s "$CONTROLLER_URL/api/sessions" | jq -r '.[] | select(.sessionId | startswith("stress-test-")) | .injectionNodeId' | sort | uniq -c
echo ""
echo "Comandi utili:"
echo "  docker logs whip-client-stress-1"
echo "  docker ps --filter 'name=whip-client-stress-*'"
echo "  docker stop \$(docker ps --filter 'name=whip-client-stress-*' -q)"
echo "  docker rm -f \$(docker ps -a --filter 'name=whip-client-stress-*' -q)"
echo ""