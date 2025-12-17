#!/bin/bash

# Test: PAD DANGLING
# Scenario:
# Configurazione Topologia base
# Crea Sessione su INJECTION
# Avvia BROADCASTER -> Flusso RTP arriva al Relay (che non ha config) -> Pad Dangling
# Configura RELAY (Late Configuration) -> Deve recuperare i pad esistenti
# Flusso arriva a egress che non trova mapping (pad dangling)
# Configurazione Egress (Session Created) -> RECOVERY.

# Funzione per pausa
pause() {
    echo ""
    echo ">>> Premi INVIO per continuare..."
    read
}

echo "TEST PAD DANGLING (LATE CONFIGURATION)"
echo ""
echo "Routing:"
echo "  pad-dangling (SSRC 5555/6666) -> egress-1"
echo ""

echo "--- CLEANUP INIZIALE ---"
docker-compose -f docker-compose.test.yaml down
docker-compose -f docker-compose.test.yaml --profile pad-dangling down

echo "--- BUILD ---"
docker-compose -f docker-compose.test.yaml build

echo "--- AVVIO INFRASTRUTTURA BASE ---"
docker-compose -f docker-compose.test.yaml up -d redis janus-videoroom janus-streaming-1
sleep 5
docker exec redis redis-cli FLUSHALL

echo "--- START NODI ---"
docker-compose -f docker-compose.test.yaml up -d injection-1 relay-1 egress-1
sleep 10

echo "--- VERIFICA NODI ---"
echo "Injection-1:"
curl -s http://localhost:7070/status | jq '{nodeId, nodeType, healthy}'
echo ""
echo "Relay-1:"
curl -s http://localhost:7071/status | jq '{nodeId, nodeType, healthy}'
echo ""
echo "Egress-1:"
curl -s http://localhost:7073/status | jq '{nodeId, nodeType, healthy}'

pause

echo ""
echo "SETUP TOPOLOGIA"
echo ""
echo "injection-1 -> relay-1 -> egress-1"
echo ""

# Topology Setup standard
docker exec redis redis-cli SADD tree:tree-1:children:injection-1 relay-1
docker exec redis redis-cli SADD tree:tree-1:parents:relay-1 injection-1
docker exec redis redis-cli PUBLISH topology:tree-1:injection-1 '{"type":"child-added","nodeId":"injection-1","childId":"relay-1"}'
docker exec redis redis-cli PUBLISH topology:tree-1:relay-1 '{"type":"parent-added","nodeId":"relay-1","parentId":"injection-1"}'

docker exec redis redis-cli SADD tree:tree-1:children:relay-1 egress-1
docker exec redis redis-cli SADD tree:tree-1:parents:egress-1 relay-1
docker exec redis redis-cli PUBLISH topology:tree-1:relay-1 '{"type":"child-added","nodeId":"relay-1","childId":"egress-1"}'
docker exec redis redis-cli PUBLISH topology:tree-1:egress-1 '{"type":"parent-added","nodeId":"egress-1","parentId":"relay-1"}'
sleep 2

pause

echo ""
echo "FASE 1: PREPARAZIONE INJECTION & EGRESS"

echo "--- CREA SESSIONE 'pad-dangling' su injection-1 ---"
curl -s -X POST http://localhost:7070/session \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "pad-dangling",
    "roomId": 2001,
    "audioSsrc": 5555,
    "videoSsrc": 6666
  }' | jq

sleep 1

echo ""
echo "FASE 2: AVVIO BROADCASTER (RTP FLOW)"
echo ""
echo "Avviamo il flusso video ORA. Il traffico passa da Injection -> Relay."
echo "Il Relay riceve RTP ma NON HA LA SESSIONE -> Pad Dangling."
echo ""

docker-compose -f docker-compose.test.yaml --profile pad-dangling up -d

sleep 5

echo "--- CHECK STATO RELAY (Dovrebbe essere vuoto o senza sessioni) ---"
# Qui ci aspettiamo che session sia null o array vuoto, ma i pacchetti stanno arrivando
curl -s http://localhost:7071/status | jq '.forwarder.sessions'

pause

echo ""
echo "FASE 3: LATE CONFIGURATION (RECOVERY)"
echo ""
echo "Ora inviamo la configurazione al Relay."
echo "Il codice C deve trovare i pad 'src_5555' e 'src_6666' già esistenti e collegarli."
echo ""

echo "--- CONTROLLER Pubblica session-created su relay-1 ---"
docker exec redis redis-cli PUBLISH sessions:tree-1:relay-1 '{
  "type": "session-created",
  "sessionId": "pad-dangling",
  "audioSsrc": 5555,
  "videoSsrc": 6666,
  "treeId": "tree-1",
  "routes": [
    {"targetId": "egress-1", "host": "egress-1", "audioPort": 5002, "videoPort": 5004}
  ]
}'

sleep 3

echo ""
echo "VERIFICA RISULTATO"
echo ""

echo "--- Relay-1 Status (Dovrebbe avere sessione attiva e target linked) ---"
curl -s http://localhost:7071/status | jq '.forwarder.sessions'

echo ""
echo "'audioTeeReady' e 'videoTeeReady' devono essere true."

pause

echo "--- CONTROLLER Configura Egress-1 ---"
docker exec redis redis-cli PUBLISH sessions:tree-1:egress-1 '{
  "type": "session-created",
  "sessionId": "pad-dangling",
  "audioSsrc": 5555,
  "videoSsrc": 6666,
  "treeId": "tree-1"
}'

sleep 2


echo ""
echo "--- Mountpoint Egress-1 ---"
curl -s http://localhost:7073/mountpoint/pad-dangling | jq

pause

echo ""
echo "--- APRI VIEWER ---"
echo ""
echo "Egress-1 (porte 7073):"
echo "  http://localhost:7073/?id=pad-dangling"
echo ""

pause

echo ""
echo "CLEANUP"

echo "--- STOP CONTAINERS ---"
docker-compose -f docker-compose.test.yaml --profile pad-dangling down
docker-compose -f docker-compose.test.yaml down

docker exec redis redis-cli FLUSHALL 2>/dev/null || true

echo ""
echo "TEST COMPLETATO"