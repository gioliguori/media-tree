
## Test 3: Scale-Out / Scale-Down - Add/Remove Egress

### Scenario Test 3.1: Scale-Out (Add Egress)

injection-1 → relay-1 → egress-1
                    ↓       (add egress-2)
injection-1 → relay-1 → [egress-1, egress-2]

### Setup Iniziale
```bash
# Stop tutto
docker-compose -f docker-compose.test.yaml down
docker-compose -f docker-compose.test.yaml --profile broadcaster down
docker-compose -f docker-compose.test.yaml --profile test-chain down
docker-compose -f docker-compose.test.yaml --profile test-scale down

docker-compose -f docker-compose.test.yaml build

# Avvio infrastruttura base
docker-compose -f docker-compose.test.yaml up -d redis janus-videoroom janus-streaming-1 janus-streaming-2
sleep 5

docker exec redis redis-cli FLUSHALL

# Start nodi (solo egress-1 inizialmente)
docker-compose -f docker-compose.test.yaml up -d injection-1 relay-1 egress-1
sleep 10

### Verifica Nodi Iniziali

curl http://localhost:7070/status | jq '{nodeId, nodeType, healthy}'
curl http://localhost:7071/status | jq '{nodeId, nodeType, healthy}'
curl http://localhost:7073/status | jq '{nodeId, nodeType, healthy}'

### Configurazione Topologia Iniziale

# injection-1 → relay-1
docker exec redis redis-cli SADD children:injection-1 relay-1
docker exec redis redis-cli SET parent:relay-1 injection-1

# relay-1 → egress-1
docker exec redis redis-cli SADD children:relay-1 egress-1
docker exec redis redis-cli SET parent:egress-1 relay-1

sleep 35

# Verifica topologia base
curl http://localhost:7070/topology | jq '.children'  # ["relay-1"]
curl http://localhost:7071/topology | jq              # {parent: "injection-1", children: ["egress-1"]}
curl http://localhost:7073/topology | jq '.parent'    # "relay-1"

### Crea Sessione

curl -X POST http://localhost:7070/session/create \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "test-scale",
    "roomId": 4001,
    "audioSsrc": 7777,
    "videoSsrc": 8888,
    "recipients": [
      {"host": "relay-1", "audioPort": 5002, "videoPort": 5004}
    ]
  }' | jq

# Expected output:
# {
#   "sessionId": "test-scale",
#   "mountpointId": 4001,
#   "whepUrl": "/whep/endpoint/test-chain",
#   "janusAudioPort": 6000,
#   "janusVideoPort": 6001
# }

  # List all sessions
curl http://localhost:7070/sessions | jq


### Crea Mountpoint su egress-1

curl -X POST http://localhost:7073/mountpoint/create \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "test-scale",
    "audioSsrc": 7777,
    "videoSsrc": 8888
  }' | jq

# Expected:
# {
#   "sessionId": "test-scale",
#   "mountpointId": 4001,
#   "janusAudioPort": 6000,
#   "janusVideoPort": 6001
# }


### Start Broadcaster

docker-compose -f docker-compose.test.yaml --profile test-scale up -d
sleep 10

# Test viewer su egress-1
open http://localhost:7073/?id=test-scale

## Verifica Flusso RTP

### tcpdump relay node 1

docker exec relay-1 apt-get update -qq && docker exec relay-1 apt-get install -y tcpdump

#  Verifica Multiplexing  relay node 1

echo "=== Audio Port 5002 - Multiplexed SSRC ==="
docker exec relay-1 timeout 10 tcpdump -i eth0 -n 'udp port 5002' -vv -x -c 30 2>&1 | grep "0x0020:"

output:
0x0020:  xxxx xxxx 0000 1e61  ← SSRC 7777 (test-chain audio)
0x0020:  xxxx xxxx 0000 1e61  ← SSRC 7777


echo "=== Video Port 5004 - Multiplexed SSRC ==="
docker exec relay-1 timeout 10 tcpdump -i eth0 -n 'udp port 5004' -vv -x -c 30 2>&1 | grep "0x0020:"

output:
0x0020:  xxxx xxxx 0000 22b8  ← SSRC 8888 (test-chain video)
0x0020:  xxxx xxxx 0000 22b8  ← SSRC 8888

##  Verifica Egress Node 

# tcpdump egress
docker exec egress-1 apt-get update -qq && docker exec egress-1 apt-get install -y tcpdump

#  Test Egress INPUT Port 5002 
echo "=== Egress INPUT Port 5002 (multiplexed) ==="
docker exec egress-1 timeout 10 tcpdump -i eth0 -n 'udp port 5002' -vv -x -c 30 2>&1 | grep "0x0020:"

#  Test Egress INPUT Port 5004
echo "=== Egress INPUT Port 5004 (multiplexed) ==="
docker exec egress-1 timeout 10 tcpdump -i eth0 -n 'udp port 5004' -vv -x -c 30 2>&1 | grep "0x0020:"


### SCALE-OUT: Add egress-2

echo "=== Adding egress-2 to topology ==="

# Start egress-2
docker-compose -f docker-compose.test.yaml up -d egress-2
sleep 10

# Verifica egress-2 running
curl http://localhost:7074/status | jq '{nodeId, nodeType, healthy}'
# Expected: {"nodeId": "egress-2", "nodeType": "egress", "healthy": true}

### Crea Mountpoint su egress-2 per STESSA Sessione

curl -X POST http://localhost:7074/mountpoint/create \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "test-scale",
    "audioSsrc": 7777,
    "videoSsrc": 8888
  }' | jq

# Expected:
# {
#   "sessionId": "test-scale",
#   "mountpointId": 4001,
#   "janusAudioPort": 6000,
#   "janusVideoPort": 6001
# }

# Aggiungi egress-2 alla topologia
docker exec redis redis-cli SADD children:relay-1 egress-2
docker exec redis redis-cli SET parent:egress-2 relay-1

sleep 35

### Verifica Topologia Aggiornata

curl http://localhost:7071/topology | jq '.children'
# Expected: ["egress-1", "egress-2"]

curl http://localhost:7074/topology | jq '.parent'
# Expected: "relay-1"

### Test Viewer su Entrambi gli Egress

# Viewer su egress-1
open http://localhost:7073/?id=test-scale

# Viewer su egress-2
open http://localhost:7074/?id=test-scale

##  Verifica Egress Node 

# tcpdump egress-2
docker exec egress-2 apt-get update -qq && docker exec egress-2 apt-get install -y tcpdump

#  Test Egress INPUT Port 5002 
echo "=== Egress INPUT Port 5002 (multiplexed) ==="
docker exec egress-2 timeout 10 tcpdump -i eth0 -n 'udp port 5002' -vv -x -c 30 2>&1 | grep "0x0020:"

#  Test Egress INPUT Port 5004
echo "=== Egress INPUT Port 5004 (multiplexed) ==="
docker exec egress-2 timeout 10 tcpdump -i eth0 -n 'udp port 5004' -vv -x -c 30 2>&1 | grep "0x0020:"

echo "=== Verify SAME SSRC on both egress ==="
docker exec egress-1 timeout 5 tcpdump -i eth0 -n 'udp port 5002' -vv -x -c 5 2>&1 | grep "0x0020:"
docker exec egress-2 timeout 5 tcpdump -i eth0 -n 'udp port 5002' -vv -x -c 5 2>&1 | grep "0x0020:"
# Expected: entrambi vedono SSRC 7777 (0x1E61)


### Verifica Entrambi i Mountpoint Attivi

curl http://localhost:7073/mountpoints | jq '.count'  # 1
curl http://localhost:7074/mountpoints | jq '.count'  # 1

# Entrambi dovrebbero vedere lo stesso stream

## 📉 Test 3.2: Scale-Down (Remove Egress)

### Scenario

injection-1 → relay-1 → [egress-1, egress-2]
                    ↓ (remove egress-1)
injection-1 → relay-1 → egress-2


Continua dal test precedente (non fare cleanup).

### 🔧 SCALE-DOWN: Remove egress-1

echo "=== Removing egress-1 from topology ==="

# Rimuovi egress-1 dalla topologia
docker exec redis redis-cli SREM children:relay-1 egress-1
docker exec redis redis-cli DEL parent:egress-1

echo "Waiting for polling cycle (30s)..."
sleep 35

### Verifica Topologia Aggiornata

curl http://localhost:7071/topology | jq '.children'
# Expected: ["egress-2"]

curl http://localhost:7073/topology | jq '.parent'
# Expected: null (egress-1 isolato)

### Verifica RTP Flow

echo "=== Verify egress-1 NO longer receives RTP ==="
docker exec egress-1 timeout 3 tcpdump -i eth0 -n 'udp port 5002' -c 10 2>&1 | tail -1
# Expected: "0 packets captured" (timeout)

echo "=== Verify egress-2 STILL receives RTP ==="
docker exec egress-2 timeout 5 tcpdump -i eth0 -n 'udp port 5002' -c 10 2>&1 | tail -1
# Expected: "10 packets captured"

### Test Viewer

# Viewer su egress-1 (freeze)
open http://localhost:7073/?id=test-scale

# Viewer su egress-2 (dovrebbe continuare a funzionare)
open http://localhost:7074/?id=test-scale

### Verifica Mountpoint Status

# egress-1 mountpoint ANCORA attivo ma non riceve RTP
curl http://localhost:7073/mountpoint/test-scale| jq '.active'
# Expected: true (ma stream non funziona!)

# egress-2 mountpoint continua a funzionare
curl http://localhost:7074/mountpoint/test-scale | jq '.active'
# Expected: true

### Cleanup Completo

# Stop broadcaster
docker-compose -f docker-compose.test.yaml --profile scale down

# Destroy mountpoints manualmente
curl -X POST http://localhost:7073/mountpoint/test-scale/destroy
curl -X POST http://localhost:7074/mountpoint/test-scale/destroy

# Destroy session
curl -X POST http://localhost:7070/session/test-scale/destroy

# Flush Redis
docker exec redis redis-cli FLUSHALL

# Stop tutto
docker-compose -f docker-compose.test.yaml down
docker-compose -f docker-compose.test.yaml --profile scale down



# Valori SSRC
# Test chain
5555 = 0x15B3 (hex)
6666 = 0x1A0A (hex)

# Test scale-out/down
7777 = 0x1E61 (hex)
8888 = 0x22B8 (hex)