
### TEST 2    Topology Change
injection-1 → relay-1 → relay-2 → egress-1
                    ↓ (remove relay-2)
injection-1 → relay-1 → egress-1
```bash

### Setup Iniziale
docker-compose -f docker-compose.test.yaml down
docker-compose -f docker-compose.test.yaml --profile broadcaster down

docker-compose -f docker-compose.test.yaml build

# Avvio infrastruttura
docker-compose -f docker-compose.test.yaml up -d redis janus-videoroom janus-streaming-1
sleep 5

docker exec redis redis-cli FLUSHALL

# Start nodi
docker-compose -f docker-compose.test.yaml up -d injection-1 relay-1 relay-2 egress-1
sleep 10

# Verifica Nodi

curl http://localhost:7070/status | jq '{nodeId, nodeType, healthy}'
curl http://localhost:7071/status | jq '{nodeId, nodeType, healthy}'
curl http://localhost:7072/status | jq '{nodeId, nodeType, healthy}'
curl http://localhost:7073/status | jq '{nodeId, nodeType, healthy}'

# Expected "healthy": true

# Configurazione Topologia

# injection-1 → relay-1
docker exec redis redis-cli SADD children:injection-1 relay-1
docker exec redis redis-cli SET parent:relay-1 injection-1

# relay-1 → relay-2
docker exec redis redis-cli SADD children:relay-1 relay-2
docker exec redis redis-cli SET parent:relay-2 relay-1

# relay-2 → egress-1
docker exec redis redis-cli SADD children:relay-2 egress-1
docker exec redis redis-cli SET parent:egress-1 relay-2

# Attendi polling cycle
sleep 35

# Verifica topologia
curl http://localhost:7070/topology | jq '.children'  # ["relay-1"]
curl http://localhost:7071/topology | jq              # {parent: "injection-1", children: ["relay-2"]}
curl http://localhost:7072/topology | jq              # {parent: "relay-1", children: ["egress-1"]}
curl http://localhost:7073/topology | jq '.parent'    # "relay-2"

### Crea Sessione e Mountpoint

# Crea sessione
curl -X POST http://localhost:7070/session/create \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "test-chain",
    "roomId": 3001,
    "audioSsrc": 5555,
    "videoSsrc": 6666,
    "recipients": [
      {"host": "relay-1", "audioPort": 5002, "videoPort": 5004}
    ]
  }' | jq

# Expected output:
# {
#   "sessionId": "test-chain",
#   "mountpointId": 3001,
#   "whepUrl": "/whep/endpoint/test-chain",
#   "janusAudioPort": 6000,
#   "janusVideoPort": 6001
# }


# Crea mountpoint

curl -X POST http://localhost:7073/mountpoint/create \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "test-chain",
    "audioSsrc": 5555,
    "videoSsrc": 6666
  }' | jq

# Expected output:
# {
#   "sessionId": "test-chain",
#   "mountpointId": 3001,  (= roomId from Redis)
#   "whepUrl": "/whep/endpoint/test-chain",
#   "janusAudioPort": 6000,
#   "janusVideoPort": 6002
# }


# List all sessions
curl http://localhost:7070/sessions | jq


# List Mountpoints
curl http://localhost:7073/mountpoints | jq


### Start Broadcaster

docker-compose -f docker-compose.test.yaml --profile test-chain up -d
sleep 10


# Test viewer
open http://localhost:7073/?id=test-chain

## Verifica Flusso RTP

### tcpdump relay node 1

docker exec relay-1 apt-get update -qq && docker exec relay-1 apt-get install -y tcpdump

#  Verifica Multiplexing  relay node 1

echo "=== Audio Port 5002 - Multiplexed SSRC ==="
docker exec relay-1 timeout 10 tcpdump -i eth0 -n 'udp port 5002' -vv -x -c 30 2>&1 | grep "0x0020:"

output:
0x0020:  xxxx xxxx 0000 15b3  ← SSRC 5555 (test-chain audio)
0x0020:  xxxx xxxx 0000 15b3  ← SSRC 5555


echo "=== Video Port 5004 - Multiplexed SSRC ==="
docker exec relay-1 timeout 10 tcpdump -i eth0 -n 'udp port 5004' -vv -x -c 30 2>&1 | grep "0x0020:"

output:
0x0020:  xxxx xxxx 0000 1a0a  ← SSRC 6666 (test-chain video)
0x0020:  xxxx xxxx 0000 1a0a  ← SSRC 6666

### tcpdump relay node 2

docker exec relay-2 apt-get update -qq && docker exec relay-2 apt-get install -y tcpdump

#  Verifica Multiplexing  relay node 2

echo "=== Audio Port 5002 - Multiplexed SSRC ==="
docker exec relay-2 timeout 10 tcpdump -i eth0 -n 'udp port 5002' -vv -x -c 30 2>&1 | grep "0x0020:"

output:
0x0020:  xxxx xxxx 0000 15b3  ← SSRC 5555 (test-chain audio)
0x0020:  xxxx xxxx 0000 15b3  ← SSRC 5555


echo "=== Video Port 5004 - Multiplexed SSRC ==="
docker exec relay-2 timeout 10 tcpdump -i eth0 -n 'udp port 5004' -vv -x -c 30 2>&1 | grep "0x0020:"

output:
0x0020:  xxxx xxxx 0000 1a0a  ← SSRC 6666 (test-chain video)
0x0020:  xxxx xxxx 0000 1a0a  ← SSRC 6666


##  Verifica Egress Node 

# tcpdump egress
docker exec egress-1 apt-get update -qq && docker exec egress-1 apt-get install -y tcpdump

#  Test Egress INPUT Port 5002 
echo "=== Egress INPUT Port 5002 (multiplexed) ==="
docker exec egress-1 timeout 10 tcpdump -i eth0 -n 'udp port 5002' -vv -x -c 30 2>&1 | grep "0x0020:"

#  Test Egress INPUT Port 5004
echo "=== Egress INPUT Port 5004 (multiplexed) ==="
docker exec egress-1 timeout 10 tcpdump -i eth0 -n 'udp port 5004' -vv -x -c 30 2>&1 | grep "0x0020:"


### 🔧 TOPOLOGY CHANGE: Remove relay-2

echo "=== Removing relay-2 from chain ==="

# Rimuovi relay-2 dalla topologia
docker exec redis redis-cli SREM children:relay-1 relay-2
docker exec redis redis-cli DEL parent:relay-2

# Collega relay-1 direttamente a egress-1
docker exec redis redis-cli SADD children:relay-1 egress-1
docker exec redis redis-cli SET parent:egress-1 relay-1

sleep 35

### Verifica Topologia Aggiornata

curl http://localhost:7071/topology | jq
# Expected: {parent: "injection-1", children: ["egress-1"]}

curl http://localhost:7073/topology | jq '.parent'
# Expected: "relay-1"

curl http://localhost:7072/topology | jq
# Expected: {parent: null, children: []} (relay-2 isolato)

# Test viewer
open http://localhost:7073/?id=test-chain

### Verifica RTP Flow Dopo Change

echo "=== Verify RTP flow dopo rimozione relay-2 ==="

# relay-1 → egress-1 (direct)
docker exec egress-1 timeout 5 tcpdump -i eth0 -n 'udp port 5002' -c 10 2>&1 | tail -1
# Expected: "10 packets captured"

# relay-2 NON dovrebbe più ricevere
docker exec relay-2 timeout 3 tcpdump -i eth0 -n 'udp port 5002' -c 10 2>&1 | tail -1
# Expected: "0 packets captured"

### Cleanup

docker-compose -f docker-compose.test.yaml --profile test-chain down
docker-compose -f docker-compose.test.yaml down