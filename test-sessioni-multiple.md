# Test: Multi-Session Multiplexing
````bash
Test completo per verificare:

Injection Node: Gestione multiple sessioni con SSRC diversi
Relay Node: RTP multiplexing
Egress Node: RTP demultiplexing con rtpssrcdemux verso Janus Streaming
WHEP Viewers: Visualizzazione stream indipendenti

### Scenario di Test

Broadcaster-1 (SSRC 1111/2222)  ──┐
                                  ├──→ Injection-1 ──→ Relay-1 (5002/5004) ──→ Egress-1
Broadcaster-2 (SSRC 3333/4444)  ──┘         ↓                                    ↓
                                         (Janus)                           (Demux SSRC)
                                                                          /            \
                                                                   Mountpoint 2001  Mountpoint 2002
                                                                   (SSRC 1111/2222) (SSRC 3333/4444)
                                                                         ↓                 ↓
                                                                    WHEP viewer-1    WHEP viewer-2

## 📋 Setup Iniziale

### 1. Avvio Infrastruttura

# Stop
docker-compose -f docker-compose.test.yaml down
docker-compose -f docker-compose.test.yaml --profile broadcaster down

# Build
docker-compose -f docker-compose.test.yaml build

# Avvio infrastruttura di base
docker-compose -f docker-compose.test.yaml up -d redis janus-videoroom janus-streaming-1
sleep 5

# Flush Redis
docker exec redis redis-cli FLUSHALL

# Start nodi
docker-compose -f docker-compose.test.yaml up -d injection-1 relay-1 egress-1
sleep 10

### 2. Verifica Nodi

curl http://localhost:7070/status | jq '{nodeId, nodeType, healthy}'
curl http://localhost:7071/status | jq '{nodeId, nodeType, healthy}'
curl http://localhost:7073/status | jq '{nodeId, nodeType, healthy}'
#### curl http://localhost:7072/status | jq '{nodeId, nodeType, healthy}'.   relay-2

# Expected: "healthy": true

### Configurazione Topologia

# injection-1 → relay-1
docker exec redis redis-cli SADD children:injection-1 relay-1
docker exec redis redis-cli SET parent:relay-1 injection-1

# relay-1 → egress-1
docker exec redis redis-cli SADD children:relay-1 egress-1
docker exec redis redis-cli SET parent:egress-1 relay-1

# polling cycle (30 seconds)
sleep 35

# Verifica topologia
curl http://localhost:7070/topology | jq '.children'  # ["relay-1"]
curl http://localhost:7071/topology | jq              # {parent: "injection-1", children: ["egress-1"]}
curl http://localhost:7073/topology | jq '.parent'    # "relay-1"
---

## Sessione 1 (broadcaster-1)

# Session 1 roomId 2001
curl -X POST http://localhost:7070/session/create \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "broadcaster-1",
    "roomId": 2001,
    "audioSsrc": 1111,
    "videoSsrc": 2222,
    "recipients": [
      {"host": "relay-1", "audioPort": 5002, "videoPort": 5004}
    ]
  }' | jq

Expected output:

{
  "sessionId": "broadcaster-1",
  "endpoint": "/whip/endpoint/broadcaster-1",
  "roomId": 1234,
  "audioSsrc": 1111,
  "videoSsrc": 2222,
  "recipients": [...]
}

## Sessione 2 (broadcaster-2)

# Session 2 roomId 2002
curl -X POST http://localhost:7070/session/create \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "broadcaster-2",
    "roomId": 2002,
    "audioSsrc": 3333,
    "videoSsrc": 4444,
    "recipients": [
      {"host": "relay-1", "audioPort": 5002, "videoPort": 5004}
    ]
  }' | jq

Expected output:

{
  "sessionId": "broadcaster-2",
  "endpoint": "/whip/endpoint/broadcaster-2",
  "roomId": 5678,
  "audioSsrc": 3333,
  "videoSsrc": 4444,
  "recipients": [...]
}

# Verify Session

# List all sessions
curl http://localhost:7070/sessions | jq

# Check Redis
docker exec redis redis-cli HGET session:broadcaster-1 audioSsrc  # → 1111
docker exec redis redis-cli HGET session:broadcaster-1 videoSsrc  # → 2222
docker exec redis redis-cli HGET session:broadcaster-2 audioSsrc  # → 3333
docker exec redis redis-cli HGET session:broadcaster-2 videoSsrc  # → 4444

docker exec redis redis-cli SMEMBERS sessions:active
# Expected: broadcaster-1, broadcaster-2

# Check injection-1 sessions
docker exec redis redis-cli SMEMBERS sessions:injection-1
# Expected: broadcaster-1, broadcaster-2

### Start Broadcasters (simple-whip-client)

# Avvio entrambi i whip client
docker-compose -f docker-compose.test.yaml --profile broadcaster up -d
sleep 10

# injection node log
docker logs injection-1

# Expected:
# [injection-1] Session created: broadcaster-1
# [injection-1] WHIP endpoint: /whip/endpoint/broadcaster-1
# [injection-1] Session created: broadcaster-2
# [injection-1] WHIP endpoint: /whip/endpoint/broadcaster-2

- **Broadcaster 1**: 
  - Video: Ball pattern (moving ball)
  - Audio: 440 Hz sine wave
  - SSRC: 1111 (audio), 2222 (video)

- **Broadcaster 2**:
  - Video: SMPTE color bars
  - Audio: 880 Hz sine wave
  - SSRC: 3333 (audio), 4444 (video)

---

## Verifica Flusso RTP

### tcpdump relay node

docker exec relay-1 apt-get update -qq && docker exec relay-1 apt-get install -y tcpdump

#  Verifica Multiplexing

echo "=== Audio Port 5002 - Multiplexed SSRC ==="
docker exec relay-1 timeout 10 tcpdump -i eth0 -n 'udp port 5002' -vv -x -c 30 2>&1 | grep "0x0020:"

output:
0x0020:  xxxx xxxx 0000 0d05  ← SSRC 3333 (broadcaster-2)
0x0020:  xxxx xxxx 0000 0457  ← SSRC 1111 (broadcaster-1)
0x0020:  xxxx xxxx 0000 0d05  ← SSRC 3333
0x0020:  xxxx xxxx 0000 0457  ← SSRC 1111


echo "=== Video Port 5004 - Multiplexed SSRC ==="
docker exec relay-1 timeout 10 tcpdump -i eth0 -n 'udp port 5004' -vv -x -c 30 2>&1 | grep "0x0020:"

output
0x0020:  xxxx xxxx 0000 08ae  ← SSRC 2222 (broadcaster-1)
0x0020:  xxxx xxxx 0000 115c  ← SSRC 4444 (broadcaster-2)


##  Verifica Egress Node 

# tcpdump egress
docker exec egress-1 apt-get update -qq && docker exec egress-1 apt-get install -y tcpdump

# Verifica Pipeline Active
curl http://localhost:7071/status | jq '.gstreamer'

#  Test Egress INPUT Port 5002 
echo "=== Egress INPUT Port 5002 (multiplexed) ==="
docker exec egress-1 timeout 10 tcpdump -i eth0 -n 'udp port 5002' -vv -x -c 30 2>&1 | grep "0x0020:"


### Mountpoint 1 (broadcaster-1)

# Crea mountpoint per sessione broadcaster-1
curl -X POST http://localhost:7073/mountpoint/create \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "broadcaster-1",
    "audioSsrc": 1111,
    "videoSsrc": 2222
  }' | jq

# Expected output:
# {
#   "sessionId": "broadcaster-1",
#   "mountpointId": 2001,  (= roomId from Redis)
#   "whepUrl": "/whep/endpoint/broadcaster-1",
#   "janusAudioPort": 6000,
#   "janusVideoPort": 6002
# }

### Verifica Mountpoint 1

# Controlla mountpoint in memoria
curl http://localhost:7073/mountpoint/broadcaster-1 | jq

# Controlla statistiche PortPool
curl http://localhost:7073/portpool | jq
# expected: allocated=2 (porta audio + video)

# Verifica pipeline avviate
curl http://localhost:7073/pipelines | jq
# expected: 
# {
#   "audio": {"running": true, "restarts": 1},
#   "video": {"running": true, "restarts": 1},
#   "mountpointsActive": 1
# }

### Mountpoint 2 (broadcaster-2)

# Crea mountpoint per sessione broadcaster-2
curl -X POST http://localhost:7073/mountpoint/create \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "broadcaster-2",
    "audioSsrc": 3333,
    "videoSsrc": 4444
  }' | jq

# Expected output:
# {
#   "sessionId": "broadcaster-2",
#   "mountpointId": 2002,
#   "whepUrl": "/whep/endpoint/broadcaster-2",
#   "janusAudioPort": 6002,
#   "janusVideoPort": 6003
# }

### VVerifica Entrambi i Mountpoint


# mountpoint count
curl http://localhost:7073/mountpoints | jq '.count'
# Expected: 2

# Check PortPool
curl http://localhost:7073/portpool | jq
# Expected: allocated=4 (2 sessions × 2 ports)

# Verifica pipelines REBUILD
curl http://localhost:7073/pipelines | jq
# Expected:
# {
#   "audio": {"running": true, "restarts": 2},  ← Rebuilt!
#   "video": {"running": true, "restarts": 2},
#   "mountpointsActive": 2
# }

### Verifica RTP Demux

# Test OUTPUT Port 6000 - Solo SSRC 1111
echo "=== Egress OUTPUT Port 6000 (solo broadcaster-1) ==="
docker exec egress-1 timeout 10 tcpdump -i eth0 -n 'udp dst port 6000' -vv -x -c 20 2>&1 | grep "0x0020:"


output:
0x0020:  xxxx xxxx 0000 0457  ← SSRC 1111

# Test OUTPUT Port 6002 - Solo SSRC 3333
echo "=== Egress OUTPUT Port 6002 (solo broadcaster-2 audio) ==="
docker exec egress-1 timeout 10 tcpdump -i eth0 -n 'udp dst port 6002' -vv -x -c 20 2>&1 | grep "0x0020:"

output :
0x0020:  xxxx xxxx 0000 0d05  ← SSRC 3333
# Test Video Demux

# Video broadcaster-1 (port 6001 - SSRC 2222)
echo "=== Egress OUTPUT Port 6001 (solo broadcaster-1) ==="
docker exec egress-1 timeout 10 tcpdump -i eth0 -n 'udp dst port 6001' -vv -x -c 20 2>&1 | grep "0x0020:"
# Expected: SOLO 08ae (SSRC 2222)

# Video broadcaster-2 (port 6003 - SSRC 4444)
echo "=== Egress OUTPUT Port 6003 (solo broadcaster-2) ==="
docker exec egress-1 timeout 10 tcpdump -i eth0 -n 'udp dst port 6003' -vv -x -c 20 2>&1 | grep "0x0020:"
# Expected: SOLO 115c (SSRC 4444)

# Verifica portpool
curl http://localhost:7073/portpool | jq

# Expected:
# {
#   "totalAllocations": 2,
#   "totalReleases": 0,
#   "allocated": 4,
#   "available": 96,
#   "capacity": 100,
#   "nextUnused": 6004
# }


# List Mountpoints
curl http://localhost:7073/mountpoints | jq

# Expected:
# {
#   "count": 2,
#   "mountpoints": [
#     {
#       "sessionId": "broadcaster-1",
#       "mountpointId": 2001,
#       "audioSsrc": 1111,
#       "videoSsrc": 2222,
#       "janusAudioPort": 6000,
#       "janusVideoPort": 6001,
#       "active": true
#     },
#     {
#       "sessionId": "broadcaster-2",
#       "mountpointId": 2002,
#       "audioSsrc": 3333,
#       "videoSsrc": 4444,
#       "janusAudioPort": 6002,
#       "janusVideoPort": 6003,
#       "active": true
#     }
#   ]
# }

### Test WHEP Viewer (broadcaster-1)
open http://localhost:7073/?id=broadcaster-1

### Test WHEP Viewer (broadcaster-2)
open http://localhost:7073/?id=broadcaster-2


##### Cleanup

# Stop broadcaster
docker-compose -f docker-compose.test.yaml --profile broadcaster down

# Destroy mountpoint
# curl -X POST http://localhost:7073/mountpoint/broadcaster-1/destroy
# curl -X POST http://localhost:7073/mountpoint/broadcaster-2/destroy

# Destroy session
# curl -X POST http://localhost:7070/session/broadcaster-1/destroy
# curl -X POST http://localhost:7070/session/broadcaster-2/destroy

# Flush Redis
docker exec redis redis-cli FLUSHALL

# Stop nodi
docker-compose -f docker-compose.test.yaml down


# Valori ssrc

1111 = 0x0457 (hex)
2222 = 0x08AE (hex)
3333 = 0x0D05 (hex)
4444 = 0x115C (hex)