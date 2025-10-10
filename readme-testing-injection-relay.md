# Testing Injection Node

## Setup Iniziale

# Rebuild tutti i container
docker-compose -f docker-compose.test.yaml build

# Start infra base
docker-compose -f docker-compose.test.yaml up -d redis janus-videoroom
sleep 3

# Flush Redis
docker exec redis redis-cli FLUSHALL

---

## SCENARIO 1: Basic Flow - WHIP → Janus → Injection → Relay → Receiver

**Flow**: `whip-client → janus-videoroom → injection-1 → relay-gst-1 → receiver-1`

### Step 1: Avvia i componenti

# Avvia injection, relay e receiver
docker-compose -f docker-compose.test.yaml up -d injection-1 relay-gst-1 receiver-1

### Step 2: Configura topologia in Redis

# Registra receiver-1
docker exec redis redis-cli HSET node:receiver-1 \
  id receiver-1 \
  type egress \
  host rtp-receiver-gst-1 \
  port 7000 \
  audioPort 6000 \
  videoPort 6002 \
  status active

docker exec redis redis-cli EXPIRE node:receiver-1 300

# Configura relazioni
# injection-1 → relay-gst-1
docker exec redis redis-cli SADD children:injection-1 relay-gst-1
docker exec redis redis-cli SET parent:relay-gst-1 injection-1

# relay-gst-1 → receiver-1
docker exec redis redis-cli SADD children:relay-gst-1 receiver-1
docker exec redis redis-cli SET parent:receiver-1 relay-gst-1

### Step 3: Verifica topologia

# Verifica relazioni
docker exec redis redis-cli SMEMBERS children:injection-1
# Output: "relay-gst-1"

docker exec redis redis-cli GET parent:relay-gst-1
# Output: "injection-1"

docker exec redis redis-cli SMEMBERS children:relay-gst-1
# Output: "receiver-1"

# Aspetta che i nodi registrino le relazioni (polling 30s)
sleep 35

### Step 4: Crea sessione WHIP

# Crea sessione sull'injection node
curl -X POST http://localhost:7070/session/create \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "live-session",
    "roomId": 1234,
    "recipient": {
      "host": "relay-gst-1",
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
#     "host": "relay-gst-1",
#     "audioPort": 5002,
#     "videoPort": 5004
#   }
# }

### Step 5: Avvia broadcaster WHIP

# Start WHIP client
docker-compose -f docker-compose.test.yaml --profile streaming up -d whip-client

# Aspetta connessione
sleep 5

### Step 6: Verifica stream

# Log injection node (dovrebbe mostrare "Publishing to WHIP endpoint")
docker logs injection-1 | tail -20

# Log relay node (dovrebbe mostrare pipeline GStreamer avviata)
docker logs relay-gst-1 | tail -20

# Log receiver (dovrebbe mostrare "Setting pipeline to PLAYING")
docker logs receiver-1 | tail -20

# Verifica pacchetti RTP sul receiver
docker exec receiver-1 sh -c 'apt-get update && apt-get install -y tcpdump'
docker exec receiver-1 tcpdump -i eth0 -n udp port 6000 -c 10

# Output atteso: pacchetti RTP in arrivo

### Step 7: Verifica status

# Status injection node
curl http://localhost:7070/status | jq

# Status relay node
curl http://localhost:7071/status | jq

# Verifica sessione attiva
curl http://localhost:7070/session | jq
# Output:
# {
#   "active": true,
#   "sessionId": "live-session",
#   "roomId": 1234,
#   "recipient": {
#     "host": "relay-gst-1",
#     "audioPort": 5002,
#     "videoPort": 5004
#   },
#   "uptime": 45
# }

---

## SCENARIO 2: Cambio Figlio - STESSO SessionID (Update Forwarder)

**Obiettivo**: Cambiare il child da `relay-gst-1` a `relay-gst-2` senza cambiare `sessionId`.

⚠️ **PROBLEMA ATTUALE**: Questo approccio **NON funziona** perché:
- `destroyEndpoint()` causa errori di race condition
- Non esiste un metodo `updateForwarder()` nella libreria
- Dobbiamo ricreare l'endpoint con un nuovo sessionId

### Test (attualmente fallisce)

# Partendo dallo Scenario 1 attivo...

# Avvia relay-gst-2
docker-compose -f docker-compose.test.yaml --profile relay-switch up -d relay-gst-2
sleep 5

# Cambia topologia in Redis
docker exec redis redis-cli DEL children:injection-1
docker exec redis redis-cli SADD children:injection-1 relay-gst-2
docker exec redis redis-cli SET parent:relay-gst-2 injection-1

docker exec redis redis-cli SADD children:relay-gst-2 receiver-1
docker exec redis redis-cli SET parent:receiver-1 relay-gst-2

# Aspetta polling
sleep 35

# Monitora log (vedrai l'errore)
docker logs -f injection-1

**Risultato atteso**: ❌ Crash con errore `"Invalid endpoint ID"`

**Motivo**: 
- `onChildrenChanged()` chiama `destroySession()`
- `destroySession()` non chiama `destroyEndpoint()` per evitare race conditions
- `createSession()` prova a creare endpoint con stesso ID
- La libreria fallisce perché l'endpoint esiste già

---

## SCENARIO 3: Cambio Figlio - NUOVO SessionID (Soluzione Workaround)

**Obiettivo**: Cambiare il child creando un nuovo endpoint con sessionId diverso.

⚠️ **Downtime**: ~2-5 secondi (broadcaster deve riconnettersi)

### Step 1: Setup iniziale

# Parti dallo Scenario 1 completo e funzionante
# (whip-client attivo, stream in corso)

### Step 2: Trigger cambio topologia

# Avvia relay-gst-2
docker-compose -f docker-compose.test.yaml --profile relay-switch up -d relay-gst-2
sleep 5

# Cambia topologia in Redis (operazione ATOMICA preferita)
docker exec redis redis-cli EVAL "redis.call('DEL', KEYS[1]); return redis.call('SADD', KEYS[1], ARGV[1])" 1 children:injection-1 relay-gst-2
docker exec redis redis-cli SET parent:relay-gst-2 injection-1

docker exec redis redis-cli SADD children:relay-gst-2 receiver-1
docker exec redis redis-cli SET parent:receiver-1 relay-gst-2

# Aspetta polling (30s + processing)
sleep 35

### Step 3: Monitora ricreazione endpoint

# Segui log injection node
docker logs -f injection-1

# Output atteso:
# 🔵 onChildrenChanged CALLED [...]
#    Added: [relay-gst-2]
#    Removed: [relay-gst-1]
# 🔒 Setting flag
# ⚠️ Child changed to relay-gst-2
# 🔄 Recreating endpoint
# 🗑️  Calling destroySession
# ✅ Session destroyed
# 🆕 Creating new session: live-session-1759941312469
# ✅ Endpoint recreated
#
# ========================================
# ENDPOINT RECREATED SUCCESSFULLY!
# ========================================
# Old ID: live-session
# New ID: live-session-1759941312469
#
# 🔧 RESTART BROADCASTER WITH NEW URL:
# docker-compose -f docker-compose.test.yaml --profile streaming run --rm \
#   -e URL=http://injection-1:7070/whip/endpoint/live-session-1759941312469 whip-client
# ========================================

### Step 4: Ricollega broadcaster

# Stop vecchio broadcaster
docker-compose -f docker-compose.test.yaml --profile streaming down

# COPIA il comando dal log (con il nuovo sessionId)
# Esempio:
docker-compose -f docker-compose.test.yaml --profile streaming run --rm \
  -e URL=http://injection-1:7070/whip/endpoint/live-session-1759941312469 whip-client

### Step 5: Verifica nuovo stream

# Verifica che relay-gst-2 riceva lo stream
docker logs relay-gst-2 | tail -20

# Verifica receiver continua a ricevere
docker exec receiver-1 tcpdump -i eth0 -n udp port 6000 -c 10

# Status aggiornato
curl http://localhost:7070/session | jq
# Output:
# {
#   "active": true,
#   "sessionId": "live-session-1759941312469",  ← Nuovo ID
#   "recipient": {
#     "host": "relay-gst-2",  ← Nuovo relay
#     ...
#   }
# }

---

## SCENARIO 4: Catena Completa con Cambio

**Flow**: `whip → janus → injection → relay-1 → relay-2 → receiver`

### Setup catena

# Start tutti i componenti
docker-compose -f docker-compose.test.yaml up -d redis janus-videoroom injection-1 relay-gst-1 relay-gst-2 receiver-1

# Flush Redis
docker exec redis redis-cli FLUSHALL
sleep 3

# Registra receiver
docker exec redis redis-cli HSET node:receiver-1 \
  id receiver-1 type egress host rtp-receiver-gst-1 port 7000 \
  audioPort 6000 videoPort 6002 status active
docker exec redis redis-cli EXPIRE node:receiver-1 300

# Topologia: injection → relay-1 → relay-2 → receiver
docker exec redis redis-cli SADD children:injection-1 relay-gst-1
docker exec redis redis-cli SET parent:relay-gst-1 injection-1

docker exec redis redis-cli SADD children:relay-gst-1 relay-gst-2
docker exec redis redis-cli SET parent:relay-gst-2 relay-gst-1

docker exec redis redis-cli SADD children:relay-gst-2 receiver-1
docker exec redis redis-cli SET parent:receiver-1 relay-gst-2

sleep 35

# Crea sessione e avvia broadcaster
curl -X POST http://localhost:7070/session/create \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "chain-session",
    "roomId": 5678,
    "recipient": {"host": "relay-gst-1", "audioPort": 5002, "videoPort": 5004}
  }' | jq

docker-compose -f docker-compose.test.yaml --profile streaming run --rm \
  -e URL=http://injection-1:7070/whip/endpoint/chain-session whip-client

### Verifica catena

# Ogni nodo dovrebbe avere pipeline attiva
docker logs relay-gst-1 | grep "pipeline"
docker logs relay-gst-2 | grep "pipeline"

# Verifica pacchetti su relay-2 (input)
docker exec relay-gst-2 sh -c 'apt-get update && apt-get install -y tcpdump'
docker exec relay-gst-2 tcpdump -i eth0 -n udp port 5102 -c 5

# Verifica pacchetti su receiver (output)
docker exec receiver-1 tcpdump -i eth0 -n udp port 6000 -c 5

---

## Troubleshooting

### Injection node non si connette a Janus

# Verifica Janus è attivo
docker logs janus-videoroom | grep "WebSocket server"

# Ping da injection a janus
docker exec injection-1 ping janus-videoroom -c 3

# Verifica env
docker exec injection-1 env | grep JANUS

### Endpoint già esistente

# Verifica sessione attiva
curl http://localhost:7070/session | jq

# Distruggi sessione manuale
curl -X POST http://localhost:7070/session/destroy

# Ricrea
curl -X POST http://localhost:7070/session/create \
  -H "Content-Type: application/json" \
  -d '{"sessionId": "new-session", "roomId": 9999, "recipient": {...}}'

### Nessun pacchetto RTP sul receiver

# Verifica GStreamer su relay
docker logs relay-gst-1 | grep "Setting pipeline"

# Verifica porte aperte
docker exec relay-gst-1 netstat -uln | grep 5002

# Test connettività
docker exec relay-gst-1 ping rtp-receiver-gst-1 -c 3

# Verifica caps RTP
docker exec receiver-1 gst-launch-1.0 \
  udpsrc port=6000 caps="application/x-rtp" ! fakesink

### Race condition su destroyEndpoint

**Sintomo**: Crash con `Error: Invalid endpoint ID`

**Causa**: La libreria janus-whip-server fa cleanup automatico quando il broadcaster si disconnette, creando race condition con la nostra chiamata manuale.

**Soluzione attuale**: Non chiamare `destroyEndpoint()` in `destroySession()` - vedi codice commentato.

**TODO**: Ricercare se esiste `updateForwarder()` o metodo simile per aggiornare il recipient senza distruggere l'endpoint.

---

## Cleanup

# Stop tutti i container
docker-compose -f docker-compose.test.yaml down
docker-compose -f docker-compose.test.yaml --profile streaming down
docker-compose -f docker-compose.test.yaml --profile relay-switch down

# Rimuovi volumi
docker volume prune -f

# Flush Redis
docker exec redis redis-cli FLUSHALL

---

## Note Tecniche

### Limiti attuali

1. **No hot-swap**: Non è possibile cambiare il recipient senza ricreare l'endpoint
2. **Downtime obbligatorio**: ~2-5 secondi quando cambia la topologia
3. **SessionID change**: Il broadcaster deve riconnettersi con un nuovo URL

### Possibili soluzioni future

1. **Metodo `updateForwarder()`**: Aggiornare solo il recipient RTP senza toccare WebRTC
2. **Dual-endpoint**: Mantenere due endpoint durante la transizione
3. **Fork libreria**: Estendere janus-whip-server con funzionalità custom

### Architettura consigliata

Per produzione, valutare:
- **Load balancer** davanti agli injection nodes per nascondere i cambi di endpoint
- **Signaling layer** per notificare broadcaster dei cambi URL senza downtime
- **Health checks** per rilevare relay down e triggerare switch automatici