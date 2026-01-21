#!/bin/bash

# Stress test con viewers
# Testa CPU Janus + Relay Root + Egress

set -e

NUM_SESSIONS=${1:-20}
VIEWERS_PER_SESSION=${2:-1}
SESSION_DELAY=15
VIEWER_DELAY=5

TREE_ID="test-1"
INJECTION_NODE="injection-1"
RELAY_ROOT="relay-root-1"

echo "STRESS TEST - CON VIEWERS"
echo "Sessioni:            $NUM_SESSIONS"
echo "Viewers/sessione:  $VIEWERS_PER_SESSION"
echo ""
echo "Componenti monitorati:"
echo "  1. Janus VideoRoom (riceve da publisher)"
echo "  2. Injection Node. js (API)"
echo "  3. Relay Root (inoltra a egress)"  
echo "  4. Egress (inoltra a viewer)"
echo ""

read -p "Premi INVIO per iniziare..."

# Cleanup
echo ""
echo "--- CLEANUP ---"
docker stop $(docker ps --filter 'name=whip-stress-' -q) 2>/dev/null || true
docker stop $(docker ps --filter 'name=viewer-stress-' -q) 2>/dev/null || true
docker rm $(docker ps -a --filter 'name=whip-stress-' -q) 2>/dev/null || true
docker rm $(docker ps -a --filter 'name=viewer-stress-' -q) 2>/dev/null || true
echo " Cleanup completato"
echo ""

# Baseline
echo "--- BASELINE ---"
sleep 5

CPU_JANUS_BASE=$(docker exec redis redis-cli HGET metrics:$TREE_ID:node:$INJECTION_NODE:janusVideoroom cpuPercent 2>/dev/null || echo "0")
CPU_INJ_BASE=$(docker exec redis redis-cli HGET metrics:$TREE_ID:node:$INJECTION_NODE:nodejs cpuPercent 2>/dev/null || echo "0")
CPU_RELAY_BASE=$(docker exec redis redis-cli HGET metrics:$TREE_ID:node:$RELAY_ROOT:nodejs cpuPercent 2>/dev/null || echo "0")

echo "CPU Janus idle:       $CPU_JANUS_BASE%"
echo "CPU Injection idle:   $CPU_INJ_BASE%"
echo "CPU Relay idle:      $CPU_RELAY_BASE%"
echo ""

# Output CSV
OUTPUT="stress-full-$(date +%Y%m%d-%H%M%S).csv"
echo "Sessions,Viewers,Publishers,CPU_Janus,CPU_Inj,CPU_Relay,BW_Relay_TX,QueueAudio,QueueVideo,Status" > $OUTPUT

# Header console
printf "%-10s | %-10s | %-10s | %-12s | %-12s | %-12s | %-15s | %-12s | %-12s | %-10s\n" \
    "Sessioni" "Viewers" "Pubs" "CPU Janus" "CPU Inj" "CPU Relay" "BW Relay TX" "Q Audio" "Q Video" "Status"
echo "---------------------------------------------------------------------------------------------------------------------------"

SATURATED=false

# Loop creazione sessioni
for i in $(seq 1 $NUM_SESSIONS); do
    SESSION_ID="stress-$i"
    
    # 1. CREA SESSIONE
    RESPONSE=$(curl -s -X POST http://localhost:8080/api/sessions \
        -H "Content-Type: application/json" \
        -d "{\"sessionId\": \"$SESSION_ID\"}")
    
    WHIP_ENDPOINT=$(echo $RESPONSE | jq -r '.whipEndpoint')
    
    # 2. PUBLISHER
    SSRC_AUDIO=$((20000 + i * 2))
    SSRC_VIDEO=$((30000 + i * 2))
    FREQ=$((440 + i * 10))
    
    docker run -d \
        --name "whip-stress-$i" \
        --network media-tree \
        --cpus="0.5" \
        --memory="256m" \
        -e "URL=$WHIP_ENDPOINT" \
        -e "TOKEN=verysecret" \
        -e "AUDIO_PIPE=audiotestsrc is-live=true wave=sine freq=$FREQ !  audioconvert ! audioresample !  queue ! opusenc ! rtpopuspay pt=111 ssrc=$SSRC_AUDIO !  queue ! application/x-rtp,media=audio,encoding-name=OPUS,payload=111" \
        -e "VIDEO_PIPE=videotestsrc is-live=true pattern=ball ! video/x-raw,width=640,height=480,framerate=30/1 ! videoconvert ! queue ! x264enc tune=zerolatency speed-preset=veryfast bitrate=2000 !  rtph264pay pt=96 ssrc=$SSRC_VIDEO config-interval=1 ! queue ! application/x-rtp,media=video,encoding-name=H264,payload=96" \
        -e "STUN_SERVER=stun://stun.l.google.com:19302" \
        -e "GST_DEBUG=0" \
        controller-whip-client-1:latest > /dev/null
    
    # Attendi publisher connesso
    sleep $SESSION_DELAY
    
    # 3. CREA VIEWERS
    for v in $(seq 1 $VIEWERS_PER_SESSION); do
        # QUESTO provisiona il path e attiva Relay Root
        VIEWER_RESPONSE=$(curl -s "http://localhost:8080/api/sessions/$SESSION_ID/view?treeId=$TREE_ID")
        
        WHEP_ENDPOINT=$(echo $VIEWER_RESPONSE | jq -r '.whepEndpoint')
        
        echo "  └─ Viewer $v:  $WHEP_ENDPOINT"
        
        sleep $VIEWER_DELAY
    done
    
    # 4. LEGGI METRICHE
    TOTAL_VIEWERS=$((i * VIEWERS_PER_SESSION))
    
    PUBLISHERS=$(curl -s http://localhost:7070/metrics 2>/dev/null | jq '[.janus.rooms[] | select(.hasPublisher == true)] | length' || echo "0")
    
    CPU_JANUS=$(docker exec redis redis-cli HGET metrics:$TREE_ID:node:$INJECTION_NODE:janusVideoroom cpuPercent 2>/dev/null || echo "N/A")
    CPU_INJ=$(docker exec redis redis-cli HGET metrics:$TREE_ID:node:$INJECTION_NODE:nodejs cpuPercent 2>/dev/null || echo "N/A")
    CPU_RELAY=$(docker exec redis redis-cli HGET metrics:$TREE_ID:node:$RELAY_ROOT:nodejs cpuPercent 2>/dev/null || echo "N/A")
    
    BW_RELAY_TX=$(docker exec redis redis-cli HGET metrics:$TREE_ID:node:$RELAY_ROOT:application bandwidthTxMbps 2>/dev/null || echo "N/A")
    
    QUEUE_AUDIO=$(docker exec redis redis-cli HGET metrics:$TREE_ID:node:$RELAY_ROOT:gstreamer maxAudioQueueMs 2>/dev/null || echo "N/A")
    QUEUE_VIDEO=$(docker exec redis redis-cli HGET metrics:$TREE_ID:node:$RELAY_ROOT:gstreamer maxVideoQueueMs 2>/dev/null || echo "N/A")
    # Status check
    STATUS="OK"
    
    if (( $(echo "$CPU_JANUS > 80" | bc -l 2>/dev/null || echo 0) )); then
        STATUS=" JANUS_SAT"
        SATURATED=true
    fi
    
    if (( $(echo "$CPU_RELAY > 80" | bc -l 2>/dev/null || echo 0) )); then
        STATUS=" RELAY_SAT"
        SATURATED=true
    fi
    
     if (( $(echo "$QUEUE_AUDIO > 200" | bc -l 2>/dev/null || echo 0) )) || (( $(echo "$QUEUE_VIDEO > 200" | bc -l 2>/dev/null || echo 0) )); then
        STATUS=" QUEUE_SAT"
        SATURATED=true
    fi
    # Console output
    printf "%-10s | %-10s | %-10s | %-12s | %-12s | %-12s | %-15s | %-12s | %-12s | %-10s\n" \
        "$i" "$TOTAL_VIEWERS" "$PUBLISHERS" "$CPU_JANUS" "$CPU_INJ" "$CPU_RELAY" "$BW_RELAY_TX" "$QUEUE_AUDIO" "$QUEUE_VIDEO" "$STATUS"
    
    # CSV
    echo "$i,$TOTAL_VIEWERS,$PUBLISHERS,$CPU_JANUS,$CPU_INJ,$CPU_RELAY,$BW_RELAY_TX,$QUEUE_AUDIO,$QUEUE_VIDEO,$STATUS" >> $OUTPUT
    
    if [ "$SATURATED" = true ]; then
        echo ""
        echo " SATURAZIONE - STOP"
        break
    fi
done

echo ""
echo "=================================================="
echo "RISULTATO FINALE"
echo "=================================================="

FINAL_SESSIONS=$(curl -s http://localhost:8080/api/sessions | jq 'length')
FINAL_PUBLISHERS=$(curl -s http://localhost:7070/metrics | jq '[.janus.rooms[] | select(.hasPublisher == true)] | length')

echo "Sessioni:        $FINAL_SESSIONS"
echo "Publishers:    $FINAL_PUBLISHERS"
echo "Viewers:       $((FINAL_SESSIONS * VIEWERS_PER_SESSION))"
echo ""

echo "CPU Janus:      $(docker exec redis redis-cli HGET metrics:$TREE_ID:node:$INJECTION_NODE:janusVideoroom cpuPercent)%"
echo "CPU Injection:  $(docker exec redis redis-cli HGET metrics:$TREE_ID:node:$INJECTION_NODE:nodejs cpuPercent)%"
echo "CPU Relay:     $(docker exec redis redis-cli HGET metrics:$TREE_ID:node:$RELAY_ROOT:nodejs cpuPercent)%"
echo "BW Relay TX:   $(docker exec redis redis-cli HGET metrics:$TREE_ID:node:$RELAY_ROOT:application bandwidthTxMbps) Mbps"
echo "Queue Audio:   $(docker exec redis redis-cli HGET metrics:$TREE_ID:node:$RELAY_ROOT:gstreamer maxAudioQueueMs) ms"
echo "Queue Video:   $(docker exec redis redis-cli HGET metrics:$TREE_ID:node:$RELAY_ROOT:gstreamer maxVideoQueueMs) ms"
echo ""

echo "Risultati:  $OUTPUT"
echo ""
echo "Cleanup:"
echo "  docker stop \$(docker ps --filter 'name=whip-stress-' -q) \$(docker ps --filter 'name=viewer-stress-' -q)"
echo "  docker rm \$(docker ps -a --filter 'name=whip-stress-' -q) \$(docker ps -a --filter 'name=viewer-stress-' -q)"
echo ""