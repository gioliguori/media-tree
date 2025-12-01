#!/bin/bash

# Test: DUPLICATION
# Scenario:
# Sessione attiva su egress-1
# Spawn egress-2
# Duplica sessione su egress-2 (mantiene egress-1)
# Verifica che funzioni su entrambi

# Funzione per pausa
pause() {
    echo ""
    echo ">>> Premi INVIO per continuare..."
    read
}

echo "TEST DUPLICATION - SESSIONE SU 2 EGRESS"
echo ""
echo "Fasi:"
echo "  1. broadcaster-1 -> egress-1"
echo "  2. Spawn egress-2"
echo "  3. Duplicazione: broadcaster-1 -> egress-1 + egress-2"
echo ""

echo "--- CLEANUP INIZIALE ---"
docker-compose -f docker-compose.test.yaml down
docker-compose -f docker-compose.test.yaml --profile broadcaster down
docker-compose -f docker-compose.test.yaml --profile scale down

echo "--- BUILD ---"
docker-compose -f docker-compose.test.yaml build

echo "--- AVVIO INFRASTRUTTURA BASE ---"
docker-compose -f docker-compose.test.yaml up -d redis janus-videoroom janus-streaming-1
sleep 5
docker exec redis redis-cli FLUSHALL

echo "--- START NODI (senza egress-2) ---"
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
echo "SETUP TOPOLOGIA INIZIALE"
echo ""
echo "injection-1 -> relay-1 -> egress-1"
echo ""

echo "--- CONFIGURAZIONE: injection-1 -> relay-1 ---"
docker exec redis redis-cli SADD tree:tree-1:children:injection-1 relay-1
docker exec redis redis-cli SET tree:tree-1:parent:relay-1 injection-1
docker exec redis redis-cli PUBLISH topology:tree-1:injection-1 '{"type":"child-added","nodeId":"injection-1","childId":"relay-1"}'
docker exec redis redis-cli PUBLISH topology:tree-1:relay-1 '{"type":"parent-changed","nodeId":"relay-1","newParent":"injection-1"}'
sleep 2

echo "--- CONFIGURAZIONE: relay-1 -> egress-1 ---"
docker exec redis redis-cli SADD tree:tree-1:children:relay-1 egress-1
docker exec redis redis-cli SET tree:tree-1:parent:egress-1 relay-1
docker exec redis redis-cli PUBLISH topology:tree-1:relay-1 '{"type":"child-added","nodeId":"relay-1","childId":"egress-1"}'
docker exec redis redis-cli PUBLISH topology:tree-1:egress-1 '{"type":"parent-changed","nodeId":"egress-1","newParent":"relay-1"}'
sleep 2

pause

echo ""
echo "FASE 1: SESSIONE SU EGRESS-1"
echo ""

echo "--- CREA SESSIONE 'broadcaster-1' su injection-1 ---"
curl -s -X POST http://localhost:7070/session \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "broadcaster-1",
    "roomId": 1001,
    "audioSsrc": 1111,
    "videoSsrc": 2222
  }' | jq

sleep 1

echo "--- CONTROLLER Configura Relay-1 -> egress-1 ---"
docker exec redis redis-cli PUBLISH sessions:tree-1:relay-1 '{
  "type": "session-created",
  "sessionId": "broadcaster-1",
  "audioSsrc": 1111,
  "videoSsrc": 2222,
  "treeId": "tree-1",
  "routes": [
    {"targetId": "egress-1", "host": "egress-1", "audioPort": 5002, "videoPort": 5004}
  ]
}'

sleep 1

echo "--- CONTROLLER Configura Egress-1 ---"
docker exec redis redis-cli PUBLISH sessions:tree-1:egress-1 '{
  "type": "session-created",
  "sessionId": "broadcaster-1",
  "audioSsrc": 1111,
  "videoSsrc": 2222,
  "treeId": "tree-1"
}'

sleep 2

echo "--- AVVIA BROADCASTER-1 ---"
docker-compose -f docker-compose.test.yaml --profile broadcaster up -d whip-client-1
sleep 10


echo "--- VERIFICA: Relay-1 ha sessione broadcaster-1 -> egress-1 ---"
curl -s http://localhost:7071/status | jq '.forwarder.sessions'

echo ""
echo "--- VERIFICA: Egress-1 ha mountpoint broadcaster-1 ---"
curl -s http://localhost:7073/mountpoint/broadcaster-1 | jq


echo "--- APRI VIEWER ---"
echo ""
echo "Egress-1 (porta 7073):"
echo "  http://localhost:7073/?id=broadcaster-1"


pause

echo ""
echo "FASE 2: SPAWN EGRESS-2"
echo ""

echo "--- AVVIA JANUS-STREAMING-2 + EGRESS-2 ---"
docker-compose -f docker-compose.test.yaml --profile scale up -d janus-streaming-2 egress-2
sleep 10

echo "--- CONTROLLER Configura Egress-2 ---"
docker exec redis redis-cli PUBLISH sessions:tree-1:egress-2 '{
  "type": "session-created",
  "sessionId": "broadcaster-1",
  "audioSsrc": 1111,
  "videoSsrc": 2222,
  "treeId": "tree-1"
}'

echo "--- VERIFICA Egress-2 ---"
curl -s http://localhost:7074/status | jq '{nodeId, nodeType, healthy}'

echo ""
echo "--- AGGIUNGI EGRESS-2 ALLA TOPOLOGIA ---"
docker exec redis redis-cli SADD tree:tree-1:children:relay-1 egress-2
docker exec redis redis-cli SET tree:tree-1:parent:egress-2 relay-1
docker exec redis redis-cli PUBLISH topology:tree-1:relay-1 '{"type":"child-added","nodeId":"relay-1","childId":"egress-2"}'
docker exec redis redis-cli PUBLISH topology:tree-1:egress-2 '{"type":"parent-changed","nodeId":"egress-2","newParent":"relay-1"}'
sleep 2

echo "--- VERIFICA TOPOLOGIA Relay-1 ---"
curl -s http://localhost:7071/topology | jq '{parent, children}'

pause

echo ""
echo "FASE 3: DUPLICATION broadcaster-1 -> egress-2"
echo ""
echo "non rimuoviamo egress-1, aggiungiamo solo egress-2"
echo ""

echo "--- CONTROLLER ADD ROUTE relay-1 -> egress-2 ---"
docker exec redis redis-cli PUBLISH sessions:tree-1:relay-1 '{
  "type": "route-added",
  "sessionId": "broadcaster-1",
  "treeId": "tree-1",
  "targetId": "egress-2"
}'

sleep 3

echo ""
echo "--- TEST Double Submit ---"
echo "Invio lo stesso comando ADD_ROUTE per egress-2."
echo "Il Relay deve ignorarlo e non creare doppi target."

docker exec redis redis-cli PUBLISH sessions:tree-1:relay-1 '{
  "type": "route-added",
  "sessionId": "broadcaster-1",
  "treeId": "tree-1",
  "targetId": "egress-2"
}'

sleep 1

echo "--- VERIFICA  ---"
echo "Atteso: 2 non 3"
echo ""
# Controlliamo che targetCount sia ancora 2 (egress-1 + egress-2) e non 3
curl -s http://localhost:7071/status | jq '.forwarder.sessions.sessions[0].targetCount'

pause 
echo ""
echo "VERIFICA DUPLICATION"
echo ""

echo "--- VERIFICA: Relay-1 ha due route attive ---"
curl -s http://localhost:7071/status | jq '.forwarder.sessions.sessions[0]'

echo "--- VERIFICA: Egress-1 ha mountpoint broadcaster-1 ---"
curl -s http://localhost:7073/mountpoint/broadcaster-1 | jq

echo ""
echo "--- VERIFICA: Egress-2 ha mountpoint broadcaster-1 ---"
curl -s http://localhost:7074/mountpoint/broadcaster-1 | jq


echo "Egress-2 (porta 7074):"
echo "  http://localhost:7074/?id=broadcaster-1"
echo ""
echo "Entrambi dovrebbero mostrare lo stesso video"
echo ""

pause

echo ""
echo "RIMOZIONE SELETTIVA"
echo ""
echo "Testiamo la rimozione di una sola route (egress-1)"
echo "mentre egress-2 continua a funzionare"
echo ""

echo "--- CONTROLLER REMOVE ROUTE relay-1 -> egress-1 ---"
docker exec redis redis-cli PUBLISH sessions:tree-1:relay-1 '{
  "type": "route-removed",
  "sessionId": "broadcaster-1",
  "treeId": "tree-1",
  "targetId": "egress-1"
}'

sleep 2

echo "--- CONTROLLER Distruggi sessione su Egress-1 ---"
docker exec redis redis-cli PUBLISH sessions:tree-1:egress-1 '{
  "type": "session-destroyed",
  "treeId": "tree-1",
  "sessionId": "broadcaster-1"
}'

sleep 2

echo "--- VERIFICA: Relay-1 ha SOLO egress-2 ---"
curl -s http://localhost:7071/status | jq '.forwarder.sessions.sessions[0]'

echo ""
echo "--- VERIFICA: Egress-1 NON ha piu mountpoint ---"
curl -s http://localhost:7073/mountpoint/broadcaster-1 2>&1 | head -5

echo ""
echo "--- VERIFICA: Egress-2 continua a funzionare ---"
curl -s http://localhost:7074/mountpoint/broadcaster-1 | jq

echo ""
echo "Egress-2 dovrebbe ancora funzionare"
echo ""

pause

echo ""
echo "CLEANUP"

echo "--- STOP CONTAINERS ---"
docker-compose -f docker-compose.test.yaml --profile broadcaster down
docker-compose -f docker-compose.test.yaml --profile scale down
docker-compose -f docker-compose.test.yaml down

docker exec redis redis-cli FLUSHALL 2>/dev/null || true

echo ""
echo "TEST COMPLETATO"