#!/bin/bash

# Script per creare singola sessione con video

set -e

# Configurazione
SESSION_ID=${1:-"manual-$(date +%s)"}
CONTROLLER_URL="http://localhost:8080"
NETWORK="media-tree"
WHIP_CLIENT_IMAGE="controller-whip-client-1:latest"

echo "=================================================="
echo "CREATE SESSION (Live Video Encoding)"
echo "=================================================="
echo "Session ID: $SESSION_ID"
echo ""

# Verifica immagine whip client
if ! docker images | grep -q "controller-whip-client-1"; then
    echo " Immagine whip client non trovata!"
    echo "Esegui: docker-compose up -d whip-client-1"
    echo ""
    echo "Oppure build manuale:"
    echo "  docker build -t controller-whip-client-1:latest ../simple-whip-client"
    exit 1
fi

# Crea sessione API
echo "--- CREAZIONE SESSIONE API ---"
RESPONSE=$(curl -s -X POST $CONTROLLER_URL/api/sessions \
    -H "Content-Type:  application/json" \
    -d "{\"sessionId\": \"$SESSION_ID\"}")

if [ $? -ne 0 ]; then
    echo " Errore creazione sessione"
    exit 1
fi

WHIP_ENDPOINT=$(echo $RESPONSE | jq -r '.whipEndpoint')
INJECTION_NODE=$(echo $RESPONSE | jq -r '.injectionNodeId')
ROOM_ID=$(echo $RESPONSE | jq -r '.roomId')

echo "  Sessione creata"
echo "  Injection:  $INJECTION_NODE"
echo "  Room ID:    $ROOM_ID"
echo "  Endpoint:   $WHIP_ENDPOINT"
echo ""

# Avvia WHIP client
echo "--- AVVIO WHIP CLIENT (live encoding) ---"

# SSRC univoci
SSRC_AUDIO=$((20000 + $(date +%s) % 10000))
SSRC_VIDEO=$((30000 + $(date +%s) % 10000))
FREQ=$((440 + RANDOM % 200))

docker run -d \
    --name "whip-$SESSION_ID" \
    --network $NETWORK \
    --cpus="0.5" \
    --memory="256m" \
    --label "com.docker.compose.project=manual" \
    --label "session=$SESSION_ID" \
    -e "URL=$WHIP_ENDPOINT" \
    -e "TOKEN=verysecret" \
    -e "AUDIO_PIPE=audiotestsrc is-live=true wave=sine freq=$FREQ ! audioconvert ! audioresample ! queue !  opusenc !  rtpopuspay pt=111 ssrc=$SSRC_AUDIO ! queue ! application/x-rtp,media=audio,encoding-name=OPUS,payload=111" \
    -e "VIDEO_PIPE=videotestsrc is-live=true pattern=ball !  video/x-raw,width=640,height=480,framerate=30/1 ! videoconvert ! queue ! x264enc tune=zerolatency speed-preset=veryfast bitrate=2000 ! rtph264pay pt=96 ssrc=$SSRC_VIDEO config-interval=1 ! queue ! application/x-rtp,media=video,encoding-name=H264,payload=96" \
    -e "STUN_SERVER=stun://stun.l.google.com:19302" \
    -e "GST_DEBUG=2" \
    --restart unless-stopped \
    $WHIP_CLIENT_IMAGE > /dev/null

if [ $? -ne 0 ]; then
    echo " Errore avvio whip client"
    # Cleanup sessione API
    curl -s -X DELETE "$CONTROLLER_URL/api/sessions/$SESSION_ID" > /dev/null
    exit 1
fi

echo "  WHIP client avviato:  whip-$SESSION_ID"
echo "  CPU limit:    0.5 (50%)"
echo "  RAM limit:   256MB"
echo "  Video:       Live H. 264 encoding (640x480@30fps, 2Mbps)"
echo "  Audio:       Sine wave ${FREQ}Hz"
echo ""

# Attendi connessione
echo "--- ATTENDO CONNESSIONE (5s) ---"
sleep 5

# Verifica publisher connesso
echo ""
echo "--- VERIFICA STATO ---"

HAS_PUBLISHER=$(docker exec redis redis-cli HGET metrics:node:$INJECTION_NODE:room:$ROOM_ID hasPublisher 2>/dev/null || echo "false")

if [ "$HAS_PUBLISHER" == "true" ]; then
    echo " STATUS: Publisher connesso"
else
    echo " STATUS: Publisher in attesa o errore (hasPublisher=$HAS_PUBLISHER)"
    echo " Controlla i log con: docker logs whip-$SESSION_ID"
fi

# CPU Janus
CPU_JANUS=$(docker exec redis redis-cli HGET metrics:node:$INJECTION_NODE:janusVideoroom cpuPercent 2>/dev/null || echo "0")
echo "CPU Janus VideoRoom:   $CPU_JANUS%"

# CPU WHIP client
CPU_WHIP=$(docker stats whip-$SESSION_ID --no-stream --format "{{.CPUPerc}}" 2>/dev/null || echo "N/A")
echo "CPU WHIP client:      $CPU_WHIP"

# Rooms totali
ROOMS_TOTAL=$(curl -s http://localhost:7070/metrics 2>/dev/null | jq '.janus.roomsActive' || echo "N/A")
PUBLISHERS_TOTAL=$(curl -s http://localhost:7070/metrics 2>/dev/null | jq '[.janus.rooms[] | select(.hasPublisher == true)] | length' || echo "N/A")
echo "Rooms attive:         $ROOMS_TOTAL"
echo "Publishers totali:    $PUBLISHERS_TOTAL"

echo " Cleanup command:"
echo " docker rm -f whip-$SESSION_ID && curl -X DELETE $CONTROLLER_URL/api/sessions/$SESSION_ID"