# Testing Injection Node

## Setup Iniziale

# Rebuild tutti i container
docker-compose -f docker-compose.test.yaml build

# Start infra base
docker-compose -f docker-compose.test.yaml up -d redis janus-videoroom janus-streaming
sleep 5

# Flush Redis
docker exec redis redis-cli FLUSHALL

---

## SCENARIO 1: Basic Flow - WHIP → Janus → Injection → Relay → Receiver

**Flow**: `whip-client → janus-videoroom → injection-1 → relay-1 → relay-2 →egress-1 → janus-streaming

### Step 1: Avvia i componenti

# Avvia injection, relay e receiver
docker-compose -f docker-compose.test.yaml up -d injection-1 relay-1  relay-2 egress-1

sleep 5

# Verifica status
curl http://localhost:7070/status | jq '{nodeId, nodeType, healthy}'
curl http://localhost:7071/status | jq '{nodeId, nodeType, healthy}'
curl http://localhost:7072/status | jq '{nodeId, nodeType, healthy}'
curl http://localhost:7073/status | jq '{nodeId, nodeType, healthy}'

### Step 2: Configura topologia in Redis

# Configura relazioni
# injection-1 → relay-1
docker exec redis redis-cli SADD children:injection-1 relay-1
docker exec redis redis-cli SET parent:relay-1 injection-1

# relay-1 → relay-2
docker exec redis redis-cli SADD children:relay-1 relay-2
docker exec redis redis-cli SET parent:relay-2 relay-1

# relay-2 → egress-1
docker exec redis redis-cli SADD children:relay-2 egress-1
docker exec redis redis-cli SET parent:egress-1 relay-2

### Step 3: Verifica topologia

# Verifica relazioni

# Verifica relazioni in Redis
docker exec redis redis-cli SMEMBERS children:injection-1
# Output: "relay-1"

docker exec redis redis-cli GET parent:relay-1
# Output: "injection-1"

docker exec redis redis-cli SMEMBERS children:relay-1
# Output: "relay-2"

docker exec redis redis-cli GET parent:relay-2
# Output: "relay-1"

docker exec redis redis-cli SMEMBERS children:relay-2
# Output: "egress-1"

docker exec redis redis-cli GET parent:egress-1
# Output: "relay-2"

# Aspetta che i nodi registrino le relazioni (polling 30s)
sleep 35

# Verifica topologia nei nodi
curl http://localhost:7070/topology | jq
# Output: {"nodeId": "injection-1", "parent": null, "children": ["relay-1"]}

curl http://localhost:7071/topology | jq
# Output: {"nodeId": "relay-1", "parent": "injection-1", "children": ["relay-2"]}

curl http://localhost:7072/topology | jq
# Output: {"nodeId": "relay-2", "parent": "relay-1", "children": ["egress-1"]}

curl http://localhost:7073/topology | jq
# Output: {"nodeId": "egress-1", "parent": "relay-2", "children": []}

# Step 4: Verifica pipeline GStreamer

# relay-1 pipelines
curl http://localhost:7071/status | jq '.gstreamer'
# Output: {"audioRunning": true, "videoRunning": true, "audioRestarts": 1, "videoRestarts": 1}

# relay-2 pipelines
curl http://localhost:7072/status | jq '.gstreamer'
# Output: {"audioRunning": true, "videoRunning": true, "audioRestarts": 1, "videoRestarts": 1}

# egress-1 pipelines
curl http://localhost:7073/pipelines | jq
# Output:
# {
#   "destination": {"host": "janus-streaming", "audioPort": 5002, "videoPort": 5004},
#   "audio": {"running": true, "restarts": 1, ...},
#   "video": {"running": true, "restarts": 1, ...}
# }

### Step 5: Crea sessione WHIP e WHEP

# Crea sessione sull'injection node
curl -X POST http://localhost:7070/session/create \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "live-session",
    "roomId": 1234,
    "recipient": {
      "host": "relay-1",
      "audioPort": 5002,
      "videoPort": 5004
    }
  }' | jq

# Output atteso:
# {
#   "sessionId": "live-session",
#   "endpoint": "/whip/endpoint/live-session",
#   "roomId": 1234,
#   "recipient": {
#     "host": "relay-1",
#     "audioPort": 5002,
#     "videoPort": 5004
#   }
# }

# Crea sessione WHEP sull'egress node
curl -X POST http://localhost:7073/session/create \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "live-session",
    "mountpointId": 1
  }' | jq

# Output atteso:
# {
#   "sessionId": "live-session",
#   "mountpointId": 1,
#   "whepUrl": "http://egress-1:7073/whep/endpoint/live-session",
#   "createdAt": 1760446820861
# }

### Step 6: Avvia broadcaster WHIP

# Start WHIP client
docker-compose -f docker-compose.test.yaml --profile streaming up -d whip-client

# Aspetta connessione
sleep 5

# Verifica log broadcaster
docker logs whip-client --tail 20


### Step 6: Verifica stream

# Log injection node (dovrebbe mostrare "Publishing to WHIP endpoint")
docker logs injection-1 | tail -20

# Log relay node (dovrebbe mostrare pipeline GStreamer avviata)
docker logs relay-1 | tail -20

# Log relay-2 (pipeline attiva)
docker logs relay-2 | tail -30

# Log egress (pipeline forwarding a Janus)
docker logs egress-1 | tail -30

# Verifica sessioni attive
curl http://localhost:7070/session | jq
# Output:
# {
#   "active": true,
#   "sessionId": "live-session",
#   "roomId": 1234,
#   "recipient": {"host": "relay-1", ...},
#   "uptime": 45
# }

curl http://localhost:7073/session | jq
# Output:
# {
#   "active": true,
#   "sessionId": "live-session",
#   "mountpointId": 1,
#   "viewers": 0,
#   "uptime": 45
# }



### Step 8: Test WHEP viewer

# Apri browser
open http://localhost:7073/?id=live-session

# Oppure usa curl per verificare endpoint WHEP
curl -I http://localhost:7073/whep/endpoint/live-session

## SCENARIO 2: Cambio Figlio - STESSO SessionID (Update Forwarder)

### Obiettivo:
Da: injection-1 → relay-1 → relay-2 → egress-1
A: injection-1 → relay-1 → egress-1

# Partendo dallo Scenario 1 attivo...

# Verifica pipeline prima del cambio
curl http://localhost:7071/status | jq '.gstreamer.audioRestarts'
# Output: 1

curl http://localhost:7073/pipelines | jq '.audio.restarts'
# Output: 1

# Step 1: Trigger cambio topologia

# Rimuovi relay-2 da relay-1 children
docker exec redis redis-cli SREM children:relay-1 relay-2

# Aggiungi egress-1 direttamente a relay-1
docker exec redis redis-cli SADD children:relay-1 egress-1

# Aggiorna parent di egress-1
docker exec redis redis-cli SET parent:egress-1 relay-1

# Verifica cambio in Redis
docker exec redis redis-cli SMEMBERS children:relay-1
# Output: "egress-1"

docker exec redis redis-cli GET parent:egress-1
# Output: "relay-1"


# Aspetta polling 

sleep 35

# Verifica topologia aggiornata
curl http://localhost:7071/topology | jq
# Output: {"parent": "injection-1", "children": ["egress-1"]}

curl http://localhost:7072/topology | jq
# Output: {"parent": "relay-1", "children": []} ← children vuoto!

curl http://localhost:7073/topology | jq
# Output: {"parent": "relay-1", "children": []}

# Step 4: Monitora ricreazione pipeline

# Segui log relay-1 (dovrebbe restartare pipeline)
docker logs -f relay-1

# Output atteso:
# [relay-1] Children changed (+1 -1)
# [relay-1] Added children: [egress-1]
# [relay-1] Removed children: [relay-2]
# [relay-1] Rebuilding GStreamer pipelines...
# [relay-1] Stopping audio pipeline...
# [relay-1] Stopping video pipeline...
# [relay-1] Starting pipelines for 1 children
# [relay-1] Starting audio pipeline (attempt #2)
# [relay-1] Audio pipeline: gst-launch-1.0 udpsrc port=5002 ... ! udpsink host=egress-1 port=5002 ...

# In un'altra shell, segui log relay-2 (pipeline dovrebbe stopparsi)
docker logs -f relay-2

# Output atteso:
# [relay-2] Children changed (+0 -1)
# [relay-2] Removed children: [egress-1]
# [relay-2] Rebuilding GStreamer pipelines...
# [relay-2] Stopping audio pipeline...
# [relay-2] Stopping video pipeline...
# [relay-2] No children, pipelines stopped

# Step 5: Verifica stream continua
# Verifica che il broadcaster sia ancora connesso
docker logs whip-client --tail 10

# Verifica sessioni ancora attive
curl http://localhost:7070/session | jq '.active'
# Output: true

curl http://localhost:7073/session | jq '.active'
# Output: true

# Verifica pipeline restarts incrementati
curl http://localhost:7071/status | jq '.gstreamer.audioRestarts'
# Output: 2 ← Incrementato!

curl http://localhost:7073/pipelines | jq '.audio.restarts'
# Output: 1 ← NON cambia (egress non ha rebuiltato)

# Verifica relay-2 pipeline fermate
curl http://localhost:7072/status | jq '.gstreamer'
# Output: {"audioRunning": false, "videoRunning": false, ...}

# Step 6: Test viewer ancora funzionante

# Ricarica pagina browser
open http://localhost:7073/?id=live-session

## SCENARIO 3: Topology Change 

# Obiettivo: Aggiungere relay-2 di nuovo nella catena.

Da: injection-1 → relay-1 → egress-1
A: injection-1 → relay-1 → relay-2 → egress-1

# Partenza dallo Scenario 2 completato
# relay-2 container già running (se non lo è):
docker-compose -f docker-compose.test.yaml --profile relay-switch up -d relay-2
sleep 5


# Step 1: Trigger cambio topologia

# Rimuovi egress-1 da relay-1 children
docker exec redis redis-cli SREM children:relay-1 egress-1

# Aggiungi relay-2 a relay-1 children
docker exec redis redis-cli SADD children:relay-1 relay-2

# Aggiungi egress-1 a relay-2 children
docker exec redis redis-cli SADD children:relay-2 egress-1

# Aggiorna parent di egress-1
docker exec redis redis-cli SET parent:egress-1 relay-2

# Verifica
docker exec redis redis-cli SMEMBERS children:relay-1
# Output: "relay-2"

docker exec redis redis-cli SMEMBERS children:relay-2
# Output: "egress-1"

# Step 2: Aspetta polling

sleep 35

# Verifica topologia
curl http://localhost:7071/topology | jq '.children'
# Output: ["relay-2"]

curl http://localhost:7072/topology | jq
# Output: {"parent": "relay-1", "children": ["egress-1"]}

curl http://localhost:7073/topology | jq '.parent'
# Output: "relay-2"



# Step 3: Verifica pipeline riavviate

# relay-1 dovrebbe restartare (child cambiato)
curl http://localhost:7071/status | jq '.gstreamer.audioRestarts'
# Output: 3 ← Incrementato di nuovo!

# relay-2 dovrebbe restartare (riaggiunta catena)
curl http://localhost:7072/status | jq '.gstreamer'
# Output: {"audioRunning": true, "videoRunning": true, "audioRestarts": 2, ...}

# egress NON dovrebbe restartare
curl http://localhost:7073/pipelines | jq '.audio.restarts'
# Output: 1 ← Invariato


# Step 6: Test viewer ancora funzionante

# Ricarica pagina browser
open http://localhost:7073/?id=live-session

## SCENARIO 4: Viewer Scale 2 Janus-streaming Instance

# Obiettivo: Distribuire i viewers su 2 istanze Janus per scalare

**Architettura**:
broadcaster → injection-1 → relay-1 → egress-1 → janus-streaming-1
                                  └ → egress-2 → janus-streaming-2

# Step 1: Avvia infra con scale-out

# Start base infra + secondo Janus + secondo egress
docker-compose -f docker-compose.test.yaml up -d redis janus-videoroom janus-streaming-1
docker-compose -f docker-compose.test.yaml --profile scale-out up -d janus-streaming-2
sleep 5

# Flush Redis
docker exec redis redis-cli FLUSHALL

# Avvia nodi
docker-compose -f docker-compose.test.yaml up -d injection-1 relay-1 egress-1
docker-compose -f docker-compose.test.yaml --profile scale up -d egress-2
sleep 5

# Step 2: Verifica istanze Janus

# Janus 1 (primary)
docker logs janus-streaming-1 | grep "WebSocket server"
curl -s http://localhost:8089/janus/info | jq

# Janus 2 (scale)
docker logs janus-streaming-2 | grep "WebSocket server"
curl -s http://localhost:8090/janus/info | jq

# Verifica status
curl http://localhost:7070/status | jq '{nodeId, nodeType, healthy}'             # injection
curl http://localhost:7071/status | jq '{nodeId, nodeType, healthy}'             # relay 
curl http://localhost:7073/status | jq '{nodeId, nodeType, healthy, janus}'      # egress 1
curl http://localhost:7074/status | jq '{nodeId, nodeType, healthy, janus}'      # egress 2

# Step 3: Setup redis

# injection → relay-1
docker exec redis redis-cli SADD children:injection-1 relay-1
docker exec redis redis-cli SET parent:relay-1 injection-1

# relay-1 → egress-1, egress-2 (fan-out)
docker exec redis redis-cli SADD children:relay-1 egress-1
docker exec redis redis-cli SADD children:relay-1 egress-2
docker exec redis redis-cli SET parent:egress-1 relay-1
docker exec redis redis-cli SET parent:egress-2 relay-1

# Aspetta polling
sleep 35

# Verifica topologia
curl http://localhost:7071/topology | jq '.children'
# Output: ["egress-1", "egress-2"]

# Step 4: Verifica pipeline

# relay-1 dovrebbe usare "tee" per splittare lo stream
docker logs relay-1 | grep -A 5 "Starting audio pipeline"

# Output atteso:
# Audio pipeline: gst-launch-1.0 udpsrc port=5002 ... ! tee name=t_audio allow-not-linked=true
#   t_audio. ! queue ... ! udpsink host=egress-1 port=5002 ...
#   t_audio. ! queue ... ! udpsink host=egress-2 port=5002 ...

# Verifica entrambi gli egress ricevono
curl http://localhost:7073/pipelines | jq '.audio.running'
# Output: true

curl http://localhost:7074/pipelines | jq '.audio.running'
# Output: true

# Step 5: Crea sessioni

# Egress 1 (janus-streaming-1, mountpoint 1)
curl -X POST http://localhost:7073/session/create \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "live-stream",
    "mountpointId": 1
  }' | jq

# Egress 2 (janus-streaming-2, mountpoint 1)
curl -X POST http://localhost:7074/session/create \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "live-stream",
    "mountpointId": 1
  }' | jq

# Nota: Stesso sessionId

# Step 6: Avvia broadcaster

# Crea sessione injection
curl -X POST http://localhost:7070/session/create \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "live-session",
    "roomId": 1234,
    "recipient": {
      "host": "relay-1",
      "audioPort": 5002,
      "videoPort": 5004
    }
  }' | jq

# Start broadcaster
docker-compose -f docker-compose.test.yaml --profile streaming up -d whip-client
sleep 8

# Step 7: Verifica RTP su entrambi i Janus

# RTP su janus-streaming-1
docker exec -it janus-streaming-1 sh -c 'apt-get update && apt-get install -y tcpdump'
docker exec janus-streaming-1 tcpdump -i eth0 -n 'udp port 5002' -c 5
# Dovrebbe vedere pacchetti RTP

# RTP su janus-streaming-2
docker exec -it janus-streaming-2 sh -c 'apt-get update && apt-get install -y tcpdump'
docker exec janus-streaming-2 tcpdump -i eth0 -n 'udp port 5002' -c 5
# Dovrebbe vedere pacchetti RTP

# Step 8: Test viewers su entrambe le istanze

# Viewer 1 → Janus 1
open http://localhost:7073/?id=live-stream

# Viewer 2 → Janus 2
open http://localhost:7074/?id=live-stream

# Session status egress-1
curl http://localhost:7073/session | jq

# Output:
# {
#   "active": true,
#   "sessionId": "live-stream",
#   "mountpointId": 1,
#   "viewers": 1,  ← Viewers su questo Janus
#   "uptime": 120
# }

# Session status egress-2
curl http://localhost:7074/session | jq

# Output:
# {
#   "active": true,
#   "sessionId": "live-stream",
#   "mountpointId": 1,
#   "viewers": 1,  ← Viewers su questo Janus
#   "uptime": 115
# }

# TOTALE viewers = 2 (distribuiti tra i 2 Janus)

# SCENARIO 4-B: Remove Scale

# Step 1: Rimuovi egress-2 dalla chain

# Rimuovi egress-2 da relay-1 children
docker exec redis redis-cli SREM children:relay-1 egress-2

# Aspetta polling
sleep 35

# Verifica topologia
curl http://localhost:7071/topology | jq '.children'
# Output: ["egress-1"]  ← Solo egress-1 rimane

# Verifica relay-1 pipeline rebuiltata
docker logs relay-1 | tail -20
# Dovrebbe mostrare: "Rebuilding GStreamer pipelines..."
# E pipeline ora usa udpsink singolo invece di tee

# Step 2: Verifica egress-1 continua a funzionare

# Session egress-1 ancora attiva
curl http://localhost:7073/session | jq '.active'
# Output: true

# Viewer su egress-1 continua a vedere stream
open http://localhost:7073/?id=live-stream

# egress-2 non riceve più RTP
curl http://localhost:7074/pipelines | jq '.audio.running'
# Output: true (pipeline running ma nessun pacchetto in arrivo)

# Step 3: Re-add egress-2

# Re-aggiungi egress-2
docker exec redis redis-cli SADD children:relay-1 egress-2

# Aspetta polling
sleep 35

# Verifica relay-1 rebuiltato con tee di nuovo
docker logs relay-1 | tail -20

# Verifica egress-2 riceve di nuovo RTP
docker exec janus-streaming-2 tcpdump -i eth0 -n 'udp port 5002' -c 5

# Test viewer su egress-2
open http://localhost:7074/?id=live-stream


## Cleanup

# Stop tutti i container
docker-compose -f docker-compose.test.yaml down
docker-compose -f docker-compose.test.yaml --profile streaming down
docker-compose -f docker-compose.test.yaml --profile relay-switch down
docker-compose -f docker-compose.test.yaml --profile scale down
# Rimuovi volumi
docker volume prune -f

# Flush Redis
docker exec redis redis-cli FLUSHALL

---



# VECCHIO TEST 2 POI CI PENSIAMO

**Obiettivo**: Cambiare il child da `relay-1` a `relay-2` senza cambiare `sessionId`.

⚠️ **PROBLEMA ATTUALE**: Questo approccio **NON funziona** perché:
- `destroyEndpoint()` causa errori di race condition
- Non esiste un metodo `updateForwarder()` nella libreria
- Dobbiamo ricreare l'endpoint con un nuovo sessionId


**Motivo**: 
- `onChildrenChanged()` chiama `destroySession()`
- `destroySession()` non chiama `destroyEndpoint()` per evitare race conditions
- `createSession()` prova a creare endpoint con stesso ID
- La libreria fallisce perché l'endpoint esiste già

---
