# Test: Multi-Session Multiplexing

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
````bash
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

docker exec redis redis-cli PUBLISH topology:injection-1 '{
  "type":"child-added",
  "nodeId":"injection-1",
  "childId":"relay-1"
}'

docker exec redis redis-cli PUBLISH topology:relay-1 '{
  "type":"parent-changed",
  "nodeId":"relay-1",
  "newParent":"injection-1"
}'

# relay-1 -> egress-1
docker exec redis redis-cli SADD children:relay-1 egress-1
docker exec redis redis-cli SET parent:egress-1 relay-1

docker exec redis redis-cli PUBLISH topology:relay-1 '{
  "type":"child-added",
  "nodeId":"relay-1",
  "childId":"egress-1"
}'

docker exec redis redis-cli PUBLISH topology:egress-1 '{
  "type":"parent-changed",
  "nodeId":"egress-1",
  "newParent":"relay-1"
}'

# polling cycle (30 seconds) sleep 35     non serve piu

# Verifica topologia
curl http://localhost:7070/topology | jq '.children'  # ["relay-1"]
curl http://localhost:7071/topology | jq              # {parent: "injection-1", children: ["egress-1"]}
curl http://localhost:7073/topology | jq '.parent'    # "relay-1"
---

## Sessione 1 (broadcaster-1)

# Session 1 roomId 2001
curl -X POST http://localhost:7070/session \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "broadcaster-1",
    "roomId": 2001,
    "audioSsrc": 1111,
    "videoSsrc": 2222
  }' | jq


# Expected output:
# {
#   "success": true,
#   "sessionId": "broadcaster-1",
#   "treeId": "injection-1",
#   "roomId": 2001,
#   "audioSsrc": 1111,
#   "videoSsrc": 2222,
#   "endpoint": "/whip/endpoint/broadcaster-1"
# }

# Step 2: Pubblica evento per creare mountpoint automaticamente
docker exec redis redis-cli PUBLISH sessions:tree:injection-1 '{
  "type": "session-created",
  "sessionId": "broadcaster-1",
  "treeId": "injection-1"
}'

sleep 2

# Verifica mountpoint creato automaticamente dall'evento
curl http://localhost:7073/mountpoint/broadcaster-1 | jq

## Sessione 2 (broadcaster-2)

# Session 2 roomId 2002
curl -X POST http://localhost:7070/session \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "broadcaster-2",
    "roomId": 2002,
    "audioSsrc": 3333,
    "videoSsrc": 4444
  }' | jq

# Expected output:
# 
# {
#   "sessionId": "broadcaster-2",
#   "endpoint": "/whip/endpoint/broadcaster-2",
#   "roomId": 5678,
#   "audioSsrc": 3333,
#   "videoSsrc": 4444,
#   "recipients": [...]
# }

# Pubblica evento
docker exec redis redis-cli PUBLISH sessions:tree:injection-1 '{
  "type": "session-created",
  "sessionId": "broadcaster-2",
  "treeId": "injection-1"
}'

sleep 2
# Verifica mountpoint creato
curl http://localhost:7073/mountpoint/broadcaster-2 | jq

# Verify Session and mountpoints

# List all sessions
curl http://localhost:7070/sessions | jq



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


### Start Broadcasters (simple-whip-client)

# Avvio entrambi i whip client
docker-compose -f docker-compose.test.yaml --profile broadcaster up -d
sleep 10

# - Broadcaster 1: 
#   - Video: Ball pattern (moving ball)
#   - Audio: 440 Hz sine wave
#   - SSRC: 1111 (audio), 2222 (video)
# 
# - Broadcaster 2:
#   - Video: SMPTE color bars
#   - Audio: 880 Hz sine wave
#   - SSRC: 3333 (audio), 4444 (video)


### Test WHEP Viewer (broadcaster-1)
open http://localhost:7073/?id=broadcaster-1

### Test WHEP Viewer (broadcaster-2)
open http://localhost:7073/?id=broadcaster-2

---

## Verifica Flusso RTP

### tcpdump relay node

docker exec relay-1 apt-get update -qq && docker exec relay-1 apt-get install -y tcpdump

#  Verifica Multiplexing

echo "=== Audio Port 5002 - Multiplexed SSRC ==="
docker exec relay-1 timeout 10 tcpdump -i eth0 -n 'udp port 5002' -vv -x -c 30 2>&1 | grep "0x0020:"

# output:
# 0x0020:  xxxx xxxx 0000 0d05  ← SSRC 3333 (broadcaster-2)
# 0x0020:  xxxx xxxx 0000 0457  ← SSRC 1111 (broadcaster-1)
# 0x0020:  xxxx xxxx 0000 0d05  ← SSRC 3333
# 0x0020:  xxxx xxxx 0000 0457  ← SSRC 1111


echo "=== Video Port 5004 - Multiplexed SSRC ==="
docker exec relay-1 timeout 10 tcpdump -i eth0 -n 'udp port 5004' -vv -x -c 30 2>&1 | grep "0x0020:"

# output
# 0x0020:  xxxx xxxx 0000 08ae  ← SSRC 2222 (broadcaster-1)
# 0x0020:  xxxx xxxx 0000 115c  ← SSRC 4444 (broadcaster-2)


##  Verifica Egress Node 

# tcpdump egress
docker exec egress-1 apt-get update -qq && docker exec egress-1 apt-get install -y tcpdump

#  Test Egress INPUT Port 5002 
echo "=== Egress INPUT Port 5002 (multiplexed) ==="
docker exec egress-1 timeout 10 tcpdump -i eth0 -n 'udp port 5002' -vv -x -c 30 2>&1 | grep "0x0020:"

# Expected: stessi SSRC del relay (1111 e 3333)

echo "=== Egress INPUT Port 5004 (multiplexed) ==="
docker exec egress-1 timeout 10 tcpdump -i eth0 -n 'udp port 5004' -vv -x -c 20 2>&1 | grep "0x0020:"

# Expected: stessi SSRC del relay (2222 e 4444)

### Verifica RTP Demux

# Test OUTPUT Port 6000 - Solo SSRC 1111
echo "=== Egress OUTPUT Port 6000 (solo broadcaster-1) ==="
docker exec egress-1 timeout 10 tcpdump -i eth0 -n 'udp dst port 6000' -vv -x -c 20 2>&1 | grep "0x0020:"


# output:
# 0x0020:  xxxx xxxx 0000 0457  ← SSRC 1111

# Test OUTPUT Port 6002 - Solo SSRC 3333
echo "=== Egress OUTPUT Port 6002 (solo broadcaster-2 audio) ==="
docker exec egress-1 timeout 10 tcpdump -i eth0 -n 'udp dst port 6002' -vv -x -c 20 2>&1 | grep "0x0020:"

# output :
# 0x0020:  xxxx xxxx 0000 0d05  ← SSRC 3333
# Test Video Demux

#  Test OUTPUT Port port 6001 - SSRC 2222)
echo "=== Egress OUTPUT Port 6001 (solo broadcaster-1) ==="
docker exec egress-1 timeout 10 tcpdump -i eth0 -n 'udp dst port 6001' -vv -x -c 20 2>&1 | grep "0x0020:"

# Expected: SOLO 08ae (SSRC 2222)

#  Test OUTPUT Port port 6003 - SSRC 4444)
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

# Injection Node Recovery
echo "=== Testing Injection Node Recovery ==="

docker-compose -f docker-compose.test.yaml restart injection-1
sleep 15

# Verifica recovery injection
curl http://localhost:7070/status | jq '{nodeId, healthy, sessions}'
curl http://localhost:7070/sessions | jq

# Expected: 2 sessioni recuperate (broadcaster-1, broadcaster-2)
# Ma broadcaster disconnessi

# RIAVVIA broadcaster per riconnessione
docker-compose -f docker-compose.test.yaml --profile broadcaster up -d

# Ora verifica stream
open http://localhost:7073/?id=broadcaster-1
open http://localhost:7073/?id=broadcaster-2

# TEST 2: Relay Node Recovery  
echo "=== Testing Relay Node Recovery ==="

docker-compose -f docker-compose.test.yaml restart relay-1
sleep 15

# Verifica recovery relay
curl http://localhost:7071/status | jq '{nodeId, healthy, forwarder}'

open http://localhost:7073/?id=broadcaster-1
open http://localhost:7073/?id=broadcaster-2
# Expected: Stream ancora visibile dopo qualche secondo di freeze

# TEST 3: Egress Node Recovery
echo "=== Testing Egress Node Recovery ==="

docker-compose -f docker-compose.test.yaml restart egress-1
sleep 15

# Verifica recovery egress
curl http://localhost:7073/status | jq '{nodeId, healthy, mountpoints, forwarder}'
curl http://localhost:7073/mountpoints | jq

# Expected: 2 mountpoints recuperati

# ricaricare la pagina
open http://localhost:7073/?id=broadcaster-1
open http://localhost:7073/?id=broadcaster-2
---

# Test crash del solo processo C
per testare :  
    docker exec -it egress-1 sh
    ps aux | grep egress-forwarder
    kill -9 processo (18 di solito) 

# Expected: Forwarder C riparte e si sincronizza

##### Cleanup

# Stop broadcaster
docker-compose -f docker-compose.test.yaml --profile broadcaster down

# Destroy session
curl -X POST http://localhost:7070/session/broadcaster-1/destroy
curl -X POST http://localhost:7070/session/broadcaster-2/destroy

# Pubblica eventi session-destroyed
docker exec redis redis-cli PUBLISH sessions:tree:injection-1 '{
  "type": "session-destroyed",
  "sessionId": "broadcaster-1",
  "treeId": "injection-1"
}'

docker exec redis redis-cli PUBLISH sessions:tree:injection-1 '{
  "type": "session-destroyed",
  "sessionId": "broadcaster-2",
  "treeId": "injection-1"
}'

sleep 2
# Flush Redis
docker exec redis redis-cli FLUSHALL

# Stop nodi
docker-compose -f docker-compose.test.yaml down


# Valori ssrc

1111 = 0x0457 (hex)
2222 = 0x08AE (hex)
3333 = 0x0D05 (hex)
4444 = 0x115C (hex)