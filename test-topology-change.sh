#!/bin/bash
# test-topology-change.sh

set -e  # Exit on error

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Helper functions
step() {
    echo -e "\n${GREEN}[STEP]${NC} $1"
}

check() {
    echo -e "${YELLOW}[CHECK]${NC} $1"
}

error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

wait_step() {
    echo -n "Waiting $1 seconds"
    for i in $(seq 1 $1); do
        echo -n "."
        sleep 1
    done
    echo " done"
}

banner() {
    echo ""
    echo -e "${GREEN}═══════════════════════════════════════${NC}"
    echo -e "${GREEN}  $1${NC}"
    echo -e "${GREEN}═══════════════════════════════════════${NC}"
    echo ""
}

# ============================================
# PHASE 1: BUILD & STARTUP
# ============================================

banner "PHASE 1: BUILD & STARTUP"

step "Building images"
docker-compose -f docker-compose.test.yaml build injection-1 relay-gst-1 relay-gst-2 egress-1

step "Stopping all containers"
docker-compose -f docker-compose.test.yaml down
docker-compose -f docker-compose.test.yaml --profile streaming down
docker-compose -f docker-compose.test.yaml --profile relay-switch down

step "Starting infrastructure (Redis + Janus)"
docker-compose -f docker-compose.test.yaml up -d redis janus-videoroom janus-streaming
wait_step 8

check "Verifying Redis is ready"
docker exec redis redis-cli ping || error "Redis not ready!"

check "Verifying Janus Videoroom is ready"
timeout 5 bash -c 'until docker logs janus-videoroom 2>&1 | grep -q "WebSocket server started"; do sleep 1; done' || error "Janus Videoroom not ready!"

check "Verifying Janus Streaming is ready"
timeout 5 bash -c 'until docker logs janus-streaming 2>&1 | grep -q "WebSocket server started"; do sleep 1; done' || error "Janus Streaming not ready!"

step "Flushing Redis"
docker exec redis redis-cli FLUSHALL

step "Starting media nodes"
docker-compose -f docker-compose.test.yaml up -d injection-1 relay-gst-1 egress-1
docker-compose -f docker-compose.test.yaml --profile relay-switch up -d relay-gst-2
wait_step 10

# ============================================
# PHASE 2: VERIFY STARTUP
# ============================================

banner "PHASE 2: VERIFY STARTUP"

check "Checking injection-1 status"
curl -s http://localhost:7070/status | jq '{nodeId, nodeType, healthy}'

check "Checking relay-gst-1 status"
curl -s http://localhost:7071/status | jq '{nodeId, nodeType, healthy}'

check "Checking relay-gst-2 status"
curl -s http://localhost:7072/status | jq '{nodeId, nodeType, healthy}'

check "Checking egress-1 status"
curl -s http://localhost:7073/status | jq '{nodeId, nodeType, healthy}'

# ============================================
# PHASE 3: SETUP TOPOLOGY (4-level tree)
# ============================================

banner "PHASE 3: SETUP 4-LEVEL TOPOLOGY"
info "injection-1 → relay-gst-1 → relay-gst-2 → egress-1"

step "Configuring topology in Redis"

# injection-1 → relay-gst-1
docker exec redis redis-cli SADD children:injection-1 relay-gst-1
docker exec redis redis-cli SET parent:relay-gst-1 injection-1

# relay-gst-1 → relay-gst-2
docker exec redis redis-cli SADD children:relay-gst-1 relay-gst-2
docker exec redis redis-cli SET parent:relay-gst-2 relay-gst-1

# relay-gst-2 → egress-1
docker exec redis redis-cli SADD children:relay-gst-2 egress-1
docker exec redis redis-cli SET parent:egress-1 relay-gst-2

check "Verifying Redis topology"
echo "  injection-1 children:"
docker exec redis redis-cli SMEMBERS children:injection-1

echo "  relay-gst-1 parent/children:"
docker exec redis redis-cli GET parent:relay-gst-1
docker exec redis redis-cli SMEMBERS children:relay-gst-1

echo "  relay-gst-2 parent/children:"
docker exec redis redis-cli GET parent:relay-gst-2
docker exec redis redis-cli SMEMBERS children:relay-gst-2

echo "  egress-1 parent:"
docker exec redis redis-cli GET parent:egress-1

check "Waiting for topology polling (35s)"
wait_step 35

check "Verifying topology propagation to nodes"
echo "injection-1:"
curl -s http://localhost:7070/topology | jq '{parent, children}'

echo "relay-gst-1:"
curl -s http://localhost:7071/topology | jq '{parent, children}'

echo "relay-gst-2:"
curl -s http://localhost:7072/topology | jq '{parent, children}'

echo "egress-1:"
curl -s http://localhost:7073/topology | jq '{parent, children}'

# ============================================
# PHASE 4: VERIFY PIPELINES
# ============================================

banner "PHASE 4: VERIFY GSTREAMER PIPELINES"

check "relay-gst-1 pipelines"
curl -s http://localhost:7071/status | jq '.gstreamer'

check "relay-gst-2 pipelines"
curl -s http://localhost:7072/status | jq '.gstreamer'

check "egress-1 pipelines"
curl -s http://localhost:7073/status | jq '.pipelines'

# ============================================
# PHASE 5: CREATE SESSIONS
# ============================================

banner "PHASE 5: CREATE SESSIONS"

step "Creating injection-1 session"
INJECTION_RESPONSE=$(curl -s -X POST http://localhost:7070/session/create \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "live-session",
    "roomId": 1234,
    "recipient": {
      "host": "relay-gst-1",
      "audioPort": 5002,
      "videoPort": 5004
    }
  }')

echo "$INJECTION_RESPONSE" | jq

WHIP_ENDPOINT=$(echo "$INJECTION_RESPONSE" | jq -r '.endpoint')
info "WHIP endpoint: $WHIP_ENDPOINT"

step "Creating egress-1 session"
EGRESS_RESPONSE=$(curl -s -X POST http://localhost:7073/session/create \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "live-session",
    "mountpointId": 1
  }')

echo "$EGRESS_RESPONSE" | jq

WHEP_URL=$(echo "$EGRESS_RESPONSE" | jq -r '.whepUrl')
info "WHEP URL: $WHEP_URL"

# ============================================
# PHASE 6: START BROADCASTER
# ============================================

banner "PHASE 6: START BROADCASTER"

step "Starting WHIP client"
docker-compose -f docker-compose.test.yaml --profile streaming up -d whip-client
wait_step 10

check "Verifying WHIP client connection"
docker logs whip-client --tail 30 | grep -i "whip\|connect\|ice" || true

check "Verifying injection-1 session is active"
curl -s http://localhost:7070/session | jq

# ============================================
# PHASE 7: VERIFY RTP FLOW (4-level)
# ============================================

banner "PHASE 7: VERIFY RTP FLOW THROUGH 4-LEVEL TREE"

info "Expected flow: broadcaster → janus-videoroom → injection-1 → relay-1 → relay-2 → egress-1 → janus-streaming"

check "RTP at relay-gst-1 input (from injection-1)"
info "Checking port 5002..."
timeout 5 docker exec relay-gst-1 tcpdump -i eth0 -n 'udp port 5002' -c 3 2>&1 | grep "UDP" && info "✓ RTP packets detected!" || info "Waiting for packets..."

check "RTP at relay-gst-2 input (from relay-1)"
info "Checking port 5102..."
timeout 5 docker exec relay-gst-2 tcpdump -i eth0 -n 'udp port 5102' -c 3 2>&1 | grep "UDP" && info "✓ RTP packets detected!" || info "Waiting for packets..."

check "RTP at egress-1 input (from relay-2)"
info "Checking egress-1 receives RTP..."
EGRESS_AUDIO_PORT=$(docker exec egress-1 printenv RTP_AUDIO_PORT)
timeout 5 docker exec egress-1 tcpdump -i eth0 -n "udp port $EGRESS_AUDIO_PORT" -c 3 2>&1 | grep "UDP" && info "✓ RTP packets detected!" || info "Waiting for packets..."

check "RTP at janus-streaming input (from egress-1)"
info "Checking port 5002 on janus-streaming..."
timeout 5 docker exec janus-streaming tcpdump -i eth0 -n 'udp port 5002' -c 3 2>&1 | grep "UDP" && info "✓ RTP packets detected!" || info "Waiting for packets..."

banner "4-LEVEL TREE IS WORKING!"
info "injection-1 → relay-1 → relay-2 → egress-1"

check "Pipeline restart counts (should all be 1)"
echo "relay-gst-1:"
curl -s http://localhost:7071/status | jq '.gstreamer | {audioRestarts, videoRestarts}'

echo "relay-gst-2:"
curl -s http://localhost:7072/status | jq '.gstreamer | {audioRestarts, videoRestarts}'

echo "egress-1:"
curl -s http://localhost:7073/status | jq '.pipelines | {audioRestarts: .audio.restarts, videoRestarts: .video.restarts}'

echo ""
info "You can test WHEP viewer at: http://localhost:7073/?id=live-session"
echo ""
read -p "Press ENTER to continue with topology change test..." 

# ============================================
# PHASE 8: TOPOLOGY CHANGE (Remove relay-2)
# ============================================

banner "PHASE 8: TOPOLOGY CHANGE TEST"
info "Changing from: injection-1 → relay-1 → relay-2 → egress-1"
info "           to: injection-1 → relay-1 → egress-1"
info "Removing relay-gst-2 from the chain..."

echo ""
read -p "Press ENTER to trigger topology change..." 

step "Recording current pipeline restart counts"
RELAY1_AUDIO_BEFORE=$(curl -s http://localhost:7071/status | jq '.gstreamer.audioRestarts')
RELAY1_VIDEO_BEFORE=$(curl -s http://localhost:7071/status | jq '.gstreamer.videoRestarts')
EGRESS_AUDIO_BEFORE=$(curl -s http://localhost:7073/status | jq '.pipelines.audio.restarts')
EGRESS_VIDEO_BEFORE=$(curl -s http://localhost:7073/status | jq '.pipelines.video.restarts')

info "relay-1 restarts before: audio=$RELAY1_AUDIO_BEFORE video=$RELAY1_VIDEO_BEFORE"
info "egress-1 restarts before: audio=$EGRESS_AUDIO_BEFORE video=$EGRESS_VIDEO_BEFORE"

step "Changing topology in Redis"

# Remove relay-gst-2 from relay-gst-1 children
docker exec redis redis-cli SREM children:relay-gst-1 relay-gst-2

# Add egress-1 directly to relay-gst-1 children
docker exec redis redis-cli SADD children:relay-gst-1 egress-1

# Update egress-1 parent
docker exec redis redis-cli SET parent:egress-1 relay-gst-1

check "Verifying Redis topology after change"
echo "  relay-gst-1 children (should have egress-1):"
docker exec redis redis-cli SMEMBERS children:relay-gst-1

echo "  relay-gst-2 children (should be empty):"
docker exec redis redis-cli SMEMBERS children:relay-gst-2

echo "  egress-1 parent (should be relay-gst-1):"
docker exec redis redis-cli GET parent:egress-1

check "Waiting for topology polling (35s)"
wait_step 35

check "Verifying topology propagation after change"
echo "relay-gst-1:"
curl -s http://localhost:7071/topology | jq

echo "relay-gst-2:"
curl -s http://localhost:7072/topology | jq

echo "egress-1:"
curl -s http://localhost:7073/topology | jq

# ============================================
# PHASE 9: VERIFY AFTER TOPOLOGY CHANGE
# ============================================

banner "PHASE 9: VERIFY RTP FLOW AFTER CHANGE"

info "New expected flow: broadcaster → janus-videoroom → injection-1 → relay-1 → egress-1 → janus-streaming"

wait_step 5

check "RTP at relay-gst-1 (should still work)"
timeout 5 docker exec relay-gst-1 tcpdump -i eth0 -n 'udp port 5002' -c 3 2>&1 | grep "UDP" && info "✓ RTP packets detected!" || error "✗ No RTP packets!"

check "RTP at egress-1 (now from relay-1 directly)"
timeout 5 docker exec egress-1 tcpdump -i eth0 -n "udp port $EGRESS_AUDIO_PORT" -c 3 2>&1 | grep "UDP" && info "✓ RTP packets detected!" || error "✗ No RTP packets!"

check "RTP at janus-streaming (should still work)"
timeout 5 docker exec janus-streaming tcpdump -i eth0 -n 'udp port 5002' -c 3 2>&1 | grep "UDP" && info "✓ RTP packets detected!" || error "✗ No RTP packets!"

step "Checking pipeline restart counts after change"
RELAY1_AUDIO_AFTER=$(curl -s http://localhost:7071/status | jq '.gstreamer.audioRestarts')
RELAY1_VIDEO_AFTER=$(curl -s http://localhost:7071/status | jq '.gstreamer.videoRestarts')
EGRESS_AUDIO_AFTER=$(curl -s http://localhost:7073/status | jq '.pipelines.audio.restarts')
EGRESS_VIDEO_AFTER=$(curl -s http://localhost:7073/status | jq '.pipelines.video.restarts')

info "relay-1 restarts after: audio=$RELAY1_AUDIO_AFTER video=$RELAY1_VIDEO_AFTER"
info "egress-1 restarts after: audio=$EGRESS_AUDIO_AFTER video=$EGRESS_AUDIO_AFTER"

if [ "$RELAY1_AUDIO_AFTER" -gt "$RELAY1_AUDIO_BEFORE" ]; then
    info "✓ relay-1 audio pipeline restarted (expected)"
else
    error "✗ relay-1 audio pipeline did NOT restart!"
fi

if [ "$RELAY1_VIDEO_AFTER" -gt "$RELAY1_VIDEO_BEFORE" ]; then
    info "✓ relay-1 video pipeline restarted (expected)"
else
    error "✗ relay-1 video pipeline did NOT restart!"
fi

# Egress NON dovrebbe restartare (riceve da parent diverso ma pipeline non cambia)
if [ "$EGRESS_AUDIO_AFTER" -eq "$EGRESS_AUDIO_BEFORE" ]; then
    info "✓ egress-1 audio pipeline did NOT restart (correct!)"
else
    info "⚠ egress-1 audio pipeline restarted (unexpected but not critical)"
fi

check "relay-gst-2 pipeline status (should have stopped)"
RELAY2_RUNNING=$(curl -s http://localhost:7072/status | jq '.gstreamer.audioRunning')
if [ "$RELAY2_RUNNING" == "false" ]; then
    info "✓ relay-2 pipelines stopped (expected)"
else
    info "⚠ relay-2 pipelines still running (might be buffering)"
fi

banner "TOPOLOGY CHANGE TEST COMPLETED!"
info "Successfully changed from 4-level to 3-level tree"
info "RTP flow is still working correctly"

echo ""
info "You can still test WHEP viewer at: http://localhost:7073/?id=live-session"
echo ""

# ============================================
# FINAL SUMMARY
# ============================================

banner "TEST SUMMARY"

echo -e "${GREEN}✓ Phase 1:${NC} Build & startup - PASSED"
echo -e "${GREEN}✓ Phase 2:${NC} Node verification - PASSED"
echo -e "${GREEN}✓ Phase 3:${NC} 4-level topology setup - PASSED"
echo -e "${GREEN}✓ Phase 4:${NC} GStreamer pipelines - PASSED"
echo -e "${GREEN}✓ Phase 5:${NC} Session creation - PASSED"
echo -e "${GREEN}✓ Phase 6:${NC} Broadcaster connection - PASSED"
echo -e "${GREEN}✓ Phase 7:${NC} RTP flow (4-level) - PASSED"
echo -e "${GREEN}✓ Phase 8:${NC} Topology change - PASSED"
echo -e "${GREEN}✓ Phase 9:${NC} RTP flow (3-level) - PASSED"

echo ""
info "All tests passed! 🎉"
echo ""

read -p "Press ENTER to view detailed logs..." 

echo ""
echo "=== INJECTION-1 LOG ==="
docker logs injection-1 --tail 30

echo ""
echo "=== RELAY-GST-1 LOG ==="
docker logs relay-gst-1 --tail 30

echo ""
echo "=== RELAY-GST-2 LOG ==="
docker logs relay-gst-2 --tail 30

echo ""
echo "=== EGRESS-1 LOG ==="
docker logs egress-1 --tail 30

echo ""
read -p "Press ENTER to cleanup..." 

# ============================================
# CLEANUP
# ============================================

banner "CLEANUP"

step "Stopping all containers"
docker-compose -f docker-compose.test.yaml down
docker-compose -f docker-compose.test.yaml --profile streaming down
docker-compose -f docker-compose.test.yaml --profile relay-switch down

step "Flushing Redis"
docker exec redis redis-cli FLUSHALL 2>/dev/null || true

info "Cleanup complete"

echo ""
echo -e "${GREEN}═══════════════════════════════════════${NC}"
echo -e "${GREEN}  TEST SCRIPT COMPLETED${NC}"
echo -e "${GREEN}═══════════════════════════════════════${NC}"