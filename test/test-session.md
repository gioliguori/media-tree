# SESSION DESTRUCTION - TEST COMPLETO

## Setup Iniziale

### Start All Services
```bash

cd ../controller
docker-compose up --build -d

## Creazione Albero e Sessione

### Crea Tree "small"

#curl -X POST http://localhost:8080/api/trees \
#  -H "Content-Type: application/json" \
#  -d '{"treeId":"default-tree","template":"small"}' | jq
#
#**Expected Response:**
#{
#  "treeId": "default-tree",
#  "template": "small",
#  "status": "active",
#  "nodes": 5
#}

### Crea Sessione

curl -X POST http://localhost:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{"sessionId":"broadcaster-1"}' | jq

**Expected Response:**
json
{
  "sessionId": "broadcaster-1",
  "treeId": "default-tree",
  "injectionNodeId": "injection-1",
  "audioSsrc": 10000,
  "videoSsrc": 10001,
  "roomId": 1000,
  "whipEndpoint": "http://injection-1:7070/whip/broadcaster-1",
  "active": true
}

### Verifica Session Metadata Redis

**Session metadata:**

docker exec redis redis-cli HGETALL tree:default-tree:session:broadcaster-1


**Expected:**

1)  "session_id"
2)  "broadcaster-1"
3)  "tree_id"
4)  "default-tree"
5)  "injection_node_id"
6)  "injection-1"
7)  "relay_root_id"
8)  "relay-root-1"
9)  "audio_ssrc"
10) "10000"
11) "video_ssrc"
12) "10001"
13) "room_id"
14) "1000"
15) "active"
16) "true"


**Session index:**

docker exec redis redis-cli SMEMBERS tree:default-tree:sessions

**Expected:** `broadcaster-1`

**Injection has session:**

docker exec redis redis-cli SMEMBERS tree:default-tree:node:injection-1:sessions

**Expected:** `broadcaster-1`

**Session dormiente creata correttamente**

---

## Provisioning Viewer (Browser)

### Viewer 1 - Apri Browser

**Azione:** Apri browser -> http://localhost:8080/

---

### Verifica Path 1 Creato (Redis)

**Egress list:**

docker exec redis redis-cli SMEMBERS tree:default-tree:session:broadcaster-1:egresses

**Expected:** `egress-1`

**Path stored:**

docker exec redis redis-cli GET tree:default-tree:session:broadcaster-1:path:egress-1

**Expected:** `injection-1,relay-root-1,egress-1`

**Relay-root has session:**

docker exec redis redis-cli SMEMBERS tree:default-tree:node:relay-root-1:sessions

**Expected:** `broadcaster-1`

**Egress-1 has session:**

docker exec redis redis-cli SMEMBERS tree:default-tree:node:egress-1:sessions

**Expected:** `broadcaster-1`

**Routing table (relay-root -> egress-1):**

docker exec redis redis-cli SMEMBERS tree:default-tree:routing:broadcaster-1:relay-root-1

**Expected:** `egress-1`

**Mountpoint egress-1:**

docker exec redis redis-cli HGETALL tree:default-tree:mountpoint:egress-1:broadcaster-1

**Expected:**

1)  "sessionId"
2)  "broadcaster-1"
3)  "mountpointId"
4)  "1000"
5)  "audioSsrc"
6)  "10000"
7)  "videoSsrc"
8)  "10001"
9)  "janusAudioPort"
10) "6000"
11) "janusVideoPort"
12) "6001"


 **Path 1 completo**

---

### Viewer 2 - Apri Altro Browser

**Azione:** Apri nuovo tab/finestra incognito -> `http://localhost:8080/`

### Verifica Path 2 Creato

**Egress list (2 egress):**

docker exec redis redis-cli SMEMBERS tree:default-tree:session:broadcaster-1:egresses

**Expected:** `egress-1` `egress-2`

**Path 2 stored:**

docker exec redis redis-cli GET tree:default-tree:session:broadcaster-1:path:egress-2

**Expected:** `injection-1,relay-root-1,egress-2`

**Egress-2 has session:**

docker exec redis redis-cli SMEMBERS tree:default-tree:node:egress-2:sessions

**Expected:** `broadcaster-1`

**Routing table aggiornata (relay-root -> egress-1, egress-2):**

docker exec redis redis-cli SMEMBERS tree:default-tree:routing:broadcaster-1:relay-root-1

**Expected:** `egress-1` `egress-2`

**Mountpoint egress-2:**

docker exec redis redis-cli HGETALL tree:default-tree:mountpoint:egress-2:broadcaster-1

**Expected:** Mountpoint creato

 **Path 2 completo - 2 path attivi**

---

## FASE 3: Invio Flusso (WHIP Client)

### 3.1 Start WHIP Client

docker-compose up -d whip-client-1


**Client invia stream a:** `http://injection-1:7070/whip/broadcaster-1`

---

### 3.2 Verifica Flusso su Egress

**Browser 1 (egress-1 - localhost:7072):**

**Browser 2 (egress-2 - localhost:7073):**

---

## Distruzione Selettiva Path

### Destroy Path egress-2

curl -X DELETE \
  "http://localhost:8080/api/trees/default-tree/sessions/broadcaster-1/egress/egress-2"


**Expected Response:**
json
{
  "status": "path_destroyed",
  "sessionId": "broadcaster-1",
  "egressId": "egress-2"
}
---

### Verifica Redis - Selective Cleanup

**Path egress-2 vuoto:**

docker exec redis redis-cli GET tree:default-tree:session:broadcaster-1:path:egress-2

**Expected:** `(nil)`

**Path egress-1 ancora presente:**

docker exec redis redis-cli GET tree:default-tree:session:broadcaster-1:path:egress-1

**Expected:** `injection-1,relay-root-1,egress-1`

**Egress list aggiornata (solo egress-1):**

docker exec redis redis-cli SMEMBERS tree:default-tree:session:broadcaster-1:egresses

**Expected:** `egress-1` (solo)

**Relay-root ha ancora session (serve egress-1):**

docker exec redis redis-cli SMEMBERS tree:default-tree:node:relay-root-1:sessions

**Expected:** `broadcaster-1` 

**Egress-2 session removed:**

docker exec redis redis-cli SMEMBERS tree:default-tree:node:egress-2:sessions

**Expected:** `(empty)`

**Routing aggiornata (solo egress-1):**

docker exec redis redis-cli SMEMBERS tree:default-tree:routing:broadcaster-1:relay-root-1

**Expected:** `egress-1` (solo)

**Mountpoint egress-2 removed:**

docker exec redis redis-cli HGETALL tree:default-tree:mountpoint:egress-2:broadcaster-1

**Expected:** `(empty)`

 **Selective cleanup corretto**

---

### Verifica Egress-1 Ancora Funzionante

**Browser 1 (egress-1 - localhost:7072):**

**Browser 2 (egress-2 - localhost:7073):**

 **Egress-1 funziona, egress-2 interrotto**

---

## Distruzione Completa Sessione

### Destroy Entire Session

curl -X DELETE "http://localhost:8080/api/trees/default-tree/sessions/broadcaster-1"


**Expected Response:**
json
{
  "status": "destroyed",
  "sessionId": "broadcaster-1",
  "treeId": "default-tree"
}
---

### Verifica Complete Cleanup Redis

**Session metadata vuota:**

docker exec redis redis-cli EXISTS tree:default-tree:session:broadcaster-1

**Expected:** `0`

**Egress list GvuotaONE:**

docker exec redis redis-cli SMEMBERS tree:default-tree:session:broadcaster-1:egresses

**Expected:** `(empty)`

**Path vuota:**

docker exec redis redis-cli GET tree:default-tree:session:broadcaster-1:path:egress-1

**Expected:** `(nil)`

**Injection session vuota:**

docker exec redis redis-cli SMEMBERS tree:default-tree:node:injection-1:sessions

**Expected:** `(empty)`

**Relay-root session vuota:**

docker exec redis redis-cli SMEMBERS tree:default-tree:node:relay-root-1:sessions

**Expected:** `(empty)`

**Egress-1 session vuota:**

docker exec redis redis-cli SMEMBERS tree:default-tree:node:egress-1:sessions

**Expected:** `(empty)`

**Routing table vuota:**

docker exec redis redis-cli SMEMBERS tree:default-tree:routing:broadcaster-1:relay-root-1

**Expected:** `(empty)`

**Session tree index vuota:**

docker exec redis redis-cli SMEMBERS tree:default-tree:sessions

**Expected:** `(empty)`

**Mountpoint egress-1 vuota:**

docker exec redis redis-cli HGETALL tree:default-tree:mountpoint:egress-1:broadcaster-1

**Expected:** `(empty)`

**Check residui (non devono esistere):**

docker exec redis redis-cli KEYS tree:default-tree:session:broadcaster-1:*

**Expected:** `(empty)`

 **Cleanup completo**

---

### Verifica Nodi

**Browser (egress-1):**
- Stream INTERROTTO (mountpoint destroyed) 

**Injection node logs:**

docker logs injection-1

**Expected:** Evento `session-destroyed` ricevuto

**Relay-root logs:**

docker logs relay-root-1

**Expected:** Evento `session-destroyed` ricevuto

**Egress-1 logs:**

docker logs egress-1

**Expected:** Evento `session-destroyed` ricevuto, mountpoint distrutto

 **Tutti nodi notificati**

---

## Cleanup

# Destroy tree
curl -X DELETE http://localhost:8080/api/trees/default-tree

# Verify Redis completamente pulito
docker exec redis redis-cli KEYS tree:default-tree:*

**Expected:** `(empty)`

 **Test completato**

---

**Reset completo:**

docker-compose down -v
