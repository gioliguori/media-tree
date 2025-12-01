#!/bin/bash

# Test: 3 Sessioni con Selective Routing
# - broadcaster-1 -> solo egress-1
# - broadcaster-2 -> solo egress-2  
# - broadcaster-3 -> entrambi (egress-1 + egress-2)

# Funzione per pausa
pause() {
    echo ""
    echo ">>> Premi INVIO per continuare"
    read
}

echo "TEST SELECTIVE ROUTING - 3 SESSIONI"
echo ""
echo "  broadcaster-1 -> egress-1"
echo "  broadcaster-2 -> egress-2"
echo "  broadcaster-3 -> egress-1 + egress-2"
echo ""

echo "--- CLEANUP INIZIALE ---"
docker-compose -f docker-compose.test.yaml down
docker-compose -f docker-compose.test.yaml --profile broadcaster down
docker-compose -f docker-compose.test.yaml --profile scale down

echo "--- BUILD ---"
docker-compose -f docker-compose.test.yaml build

echo "--- AVVIO INFRASTRUTTURA BASE ---"
docker-compose -f docker-compose.test.yaml up -d redis janus-videoroom janus-streaming-1 janus-streaming-2
sleep 5
docker exec redis redis-cli FLUSHALL

echo "--- START NODI ---"
docker-compose -f docker-compose.test.yaml up -d injection-1 relay-1 egress-1 egress-2
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
echo ""
echo "Egress-2:"
curl -s http://localhost:7074/status | jq '{nodeId, nodeType, healthy}'

pause

echo ""
echo "SETUP TOPOLOGIA"
echo ""
echo "injection-1 -> relay-1 -> egress-1"
echo "                       -> egress-2"
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

echo "--- CONFIGURAZIONE: relay-1 -> egress-2 ---"
docker exec redis redis-cli SADD tree:tree-1:children:relay-1 egress-2
docker exec redis redis-cli SET tree:tree-1:parent:egress-2 relay-1

docker exec redis redis-cli PUBLISH topology:tree-1:relay-1 '{"type":"child-added","nodeId":"relay-1","childId":"egress-2"}'
docker exec redis redis-cli PUBLISH topology:tree-1:egress-2 '{"type":"parent-changed","nodeId":"egress-2","newParent":"relay-1"}'
sleep 2

echo "--- VERIFICA TOPOLOGIA ---"
echo "Injection-1 children:"
curl -s http://localhost:7070/topology | jq '.children'
echo ""
echo "Relay-1 topology:"
curl -s http://localhost:7071/topology | jq '{parent, children}'
echo ""
echo "Egress-1 parent:"
curl -s http://localhost:7073/topology | jq '.parent'
echo ""
echo "Egress-2 parent:"
curl -s http://localhost:7074/topology | jq '.parent'

pause

echo ""
echo "SESSIONE 1: broadcaster-1 -> egress-1"
echo ""

echo "--- CREA SESSIONE broadcaster-1 su injection-1 ---"
curl -s -X POST http://localhost:7070/session \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "broadcaster-1",
    "roomId": 2001,
    "audioSsrc": 1111,
    "videoSsrc": 2222
  }' | jq

sleep 2

echo "--- CONTROLLER Pubblica session-created su relay-1 ---"
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

sleep 2

echo "--- CONTROLLER Pubblica session-created su egress-1 ---"
docker exec redis redis-cli PUBLISH sessions:tree-1:egress-1 '{
  "type": "session-created",
  "sessionId": "broadcaster-1",
  "audioSsrc": 1111,
  "videoSsrc": 2222,
  "treeId": "tree-1"
}'

sleep 2

echo "--- VERIFICA broadcaster-1 ---"
echo "Mountpoint egress-1:"
curl -s http://localhost:7073/mountpoint/broadcaster-1 | jq
echo ""
echo "Mountpoint egress-2 dovrebbe essere vuoto:"
curl -s http://localhost:7074/mountpoint/broadcaster-1 | jq

pause

echo ""
echo "SESSIONE 2: broadcaster-2 -> egress-2"
echo ""

echo "--- CREA SESSIONE broadcaster-2 su injection-1 ---"
curl -s -X POST http://localhost:7070/session \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "broadcaster-2",
    "roomId": 2002,
    "audioSsrc": 3333,
    "videoSsrc": 4444
  }' | jq

sleep 2

echo "--- CONTROLLER Pubblica session-created su relay-1 ---"
docker exec redis redis-cli PUBLISH sessions:tree-1:relay-1 '{
  "type": "session-created",
  "sessionId": "broadcaster-2",
  "audioSsrc": 3333,
  "videoSsrc": 4444,
  "treeId": "tree-1",
  "routes": [
    {"targetId": "egress-2", "host": "egress-2", "audioPort": 5002, "videoPort": 5004}
  ]
}'

sleep 2

echo "--- CONTROLLER Pubblica session-created su egress-2 ---"
docker exec redis redis-cli PUBLISH sessions:tree-1:egress-2 '{
  "type": "session-created",
  "sessionId": "broadcaster-2",
  "audioSsrc": 3333,
  "videoSsrc": 4444,
  "treeId": "tree-1"
}'

sleep 2

echo "--- VERIFICA broadcaster-2 ---"
echo "Mountpoint egress-1 (dovrebbe essere vuoto):"
curl -s http://localhost:7073/mountpoint/broadcaster-2 | jq
echo ""
echo "Mountpoint egress-2:"
curl -s http://localhost:7074/mountpoint/broadcaster-2 | jq

pause

echo ""
echo "SESSIONE 3: broadcaster-3 -> entrambi"
echo ""

echo "--- CREA SESSIONE broadcaster-3 su injection-1 ---"
curl -s -X POST http://localhost:7070/session \
  -H "Content-Type: application/json" \
  -d '{
    "sessionId": "broadcaster-3",
    "roomId": 2003,
    "audioSsrc": 5555,
    "videoSsrc": 6666
  }' | jq

sleep 2

echo "--- CONTROLLER Pubblica session-created su relay-1 (2 route) ---"
docker exec redis redis-cli PUBLISH sessions:tree-1:relay-1 '{
  "type": "session-created",
  "sessionId": "broadcaster-3",
  "audioSsrc": 5555,
  "videoSsrc": 6666,
  "treeId": "tree-1",
  "routes": [
    {"targetId": "egress-1", "host": "egress-1", "audioPort": 5002, "videoPort": 5004},
    {"targetId": "egress-2", "host": "egress-2", "audioPort": 5002, "videoPort": 5004}
  ]
}'

sleep 2

echo "--- CONTROLLER Pubblica session-created su egress-1 ---"
docker exec redis redis-cli PUBLISH sessions:tree-1:egress-1 '{
  "type": "session-created",
  "sessionId": "broadcaster-3",
  "audioSsrc": 5555,
  "videoSsrc": 6666,
  "treeId": "tree-1"
}'

sleep 2

echo "--- CONTROLLER Pubblica session-created su egress-2 ---"
docker exec redis redis-cli PUBLISH sessions:tree-1:egress-2 '{
  "type": "session-created",
  "sessionId": "broadcaster-3",
  "audioSsrc": 5555,
  "videoSsrc": 6666,
  "treeId": "tree-1"
}'

sleep 2

echo "--- VERIFICA broadcaster-3 ---"
echo "Mountpoint egress-1:"
curl -s http://localhost:7073/mountpoint/broadcaster-3 | jq
echo ""
echo "Mountpoint egress-2:"
curl -s http://localhost:7074/mountpoint/broadcaster-3 | jq

pause

echo ""
echo "VERIFICA STATO RELAY-1"
echo ""

echo "--- Relay-1 dovrebbe avere 3 sessioni con routing diverso ---"
curl -s http://localhost:7071/status | jq '.forwarder.sessions'

echo ""
echo "Atteso:"
echo "  broadcaster-1: 1 target (egress-1)"
echo "  broadcaster-2: 1 target (egress-2)"
echo "  broadcaster-3: 2 targets (egress-1, egress-2)"

pause

echo ""
echo "VERIFICA STATO EGRESS"
echo ""

echo "--- Egress-1 mountpoints ---"
curl -s http://localhost:7073/mountpoints | jq
echo ""
echo "Atteso: broadcaster-1, broadcaster-3"

echo ""
echo "--- Egress-2 mountpoints ---"
curl -s http://localhost:7074/mountpoints | jq
echo ""
echo "Atteso: broadcaster-2, broadcaster-3"

pause

echo ""
echo "START BROADCASTERS"
echo ""

docker-compose -f docker-compose.test.yaml --profile broadcaster up -d
sleep 10

echo "--- APRI VIEWER ---"
echo ""
echo "Egress-1 (porte 7073):"
echo "  http://localhost:7073/?id=broadcaster-1  (solo qui)"
echo "  http://localhost:7073/?id=broadcaster-3  (anche su egress-2)"
echo ""
echo "Egress-2 (porta 7074):"
echo "  http://localhost:7074/?id=broadcaster-2  (solo qui)"
echo "  http://localhost:7074/?id=broadcaster-3  (anche su egress-1)"
echo ""

pause

echo ""
echo "TEST RECOVERY: Injection Node"
echo ""

echo "--- RESTART injection-1 ---"
docker-compose -f docker-compose.test.yaml kill injection-1
docker-compose -f docker-compose.test.yaml start injection-1
sleep 10

echo "--- VERIFICA RECOVERY injection-1 ---"
curl -s http://localhost:7070/status | jq '{nodeId, healthy, sessions}'
echo ""
curl -s http://localhost:7070/sessions | jq

echo "--- RIAVVIA BROADCASTERS---"
docker-compose -f docker-compose.test.yaml --profile broadcaster up -d
sleep 10

pause

echo ""
echo "TEST RECOVERY: Egress-1"
echo ""

echo "--- RESTART egress-1 ---"
docker-compose -f docker-compose.test.yaml kill egress-1
docker-compose -f docker-compose.test.yaml start egress-1
sleep 15

echo "--- VERIFICA RECOVERY egress-1 ---"
curl -s http://localhost:7073/status | jq '{nodeId, healthy, mountpoints}'
echo ""
curl -s http://localhost:7073/mountpoints | jq

echo "--- Stream su egress-1 dovrebbero essere visibili ---"
echo "  http://localhost:7073/?id=broadcaster-1"
echo "  http://localhost:7073/?id=broadcaster-3"

pause

echo ""
echo "TEST RECOVERY: Egress-2"
echo ""

echo "--- RESTART egress-2 ---"
docker-compose -f docker-compose.test.yaml kill egress-2
docker-compose -f docker-compose.test.yaml start egress-2
sleep 15

echo "--- VERIFICA RECOVERY egress-2 ---"
curl -s http://localhost:7074/status | jq '{nodeId, healthy, mountpoints}'
echo ""
curl -s http://localhost:7074/mountpoints | jq

echo "--- Stream su egress-2 dovrebbero essere visibili ---"
echo "  http://localhost:7074/?id=broadcaster-2"
echo "  http://localhost:7074/?id=broadcaster-3"

pause

echo ""
echo "TEST RIMOZIONE SINGOLA ROUTE"
echo ""
echo "Scenario: broadcaster-3 non va più su egress-2"
echo ""

echo "--- CONTROLLER route-removed su relay-1 ---"
docker exec redis redis-cli PUBLISH sessions:tree-1:relay-1 '{
  "type": "route-removed",
  "sessionId": "broadcaster-3",
  "treeId": "tree-1",
  "targetId": "egress-2"
}' 
sleep 2

#echo "--- CONTROLLER session-destroyed su egress-2 ---"
#docker exec redis redis-cli PUBLISH sessions:tree-1:egress-2 '{
#  "type": "session-destroyed",
#  "sessionId": "broadcaster-3",
#  "treeId": "tree-1"
#}'
#
#sleep 2

echo "--- VERIFICA STATO RELAY-1 ---"
curl -s http://localhost:7071/status | jq '.forwarder.sessions.sessions[] | select(.sessionId == "broadcaster-3")'
echo ""
echo "Atteso: broadcaster-3 ora ha solo 1 target (egress-1)"

echo ""
echo "--- VERIFICA MOUNTPOINT ---"
echo "Egress-1 (broadcaster-3 ancora presente):"
curl -s http://localhost:7073/mountpoint/broadcaster-3 | jq
echo ""
echo "Egress-2 (broadcaster-3 rimosso):"
curl -s http://localhost:7074/mountpoint/broadcaster-3 | jq

pause

echo ""
echo "TEST AGGIUNTA ROUTE"
echo ""
echo "Scenario: broadcaster-3 torna anche su egress-2"
echo ""

echo "--- CONTROLLER route-added su relay-1 ---"
docker exec redis redis-cli PUBLISH sessions:tree-1:relay-1 '{
  "type": "route-added",
  "sessionId": "broadcaster-3",
  "treeId": "tree-1",
  "targetId": "egress-2"
}'

sleep 2

#echo "--- CONTROLLER session-created su egress-2 ---"
#docker exec redis redis-cli PUBLISH sessions:tree-1:egress-2 '{
#  "type": "session-created",
#  "sessionId": "broadcaster-3",
#  "audioSsrc": 5555,
#  "videoSsrc": 6666,
#  "treeId": "tree-1"
#}'

sleep 2

echo "--- VERIFICA STATO RELAY-1 ---"
curl -s http://localhost:7071/status | jq '.forwarder.sessions.sessions[] | select(.sessionId == "broadcaster-3")'
echo ""
echo "Atteso: broadcaster-3 ora ha 2 targets di nuovo"

echo ""
echo "--- VERIFICA MOUNTPOINT ---"
echo "Egress-2 (broadcaster-3 ri-aggiunto):"
curl -s http://localhost:7074/mountpoint/broadcaster-3 | jq

echo ""
echo "Ricarica viewer:"
echo "  http://localhost:7074/?id=broadcaster-3"

pause

echo ""
echo "CLEANUP FINALE"
echo ""

echo "--- DESTROY SESSIONS ---"
curl -s -X POST http://localhost:7070/session/broadcaster-1/destroy | jq
curl -s -X POST http://localhost:7070/session/broadcaster-2/destroy | jq
curl -s -X POST http://localhost:7070/session/broadcaster-3/destroy | jq

echo "--- CONTROLLER session-destroyed (BROADCAST) ---"
docker exec redis redis-cli PUBLISH sessions:tree-1 '{
  "type": "session-destroyed",
  "sessionId": "broadcaster-1",
  "treeId": "tree-1"
}'
docker exec redis redis-cli PUBLISH sessions:tree-1 '{
  "type": "session-destroyed",
  "sessionId": "broadcaster-2",
  "treeId": "tree-1"
}'
docker exec redis redis-cli PUBLISH sessions:tree-1 '{
  "type": "session-destroyed",
  "sessionId": "broadcaster-3",
  "treeId": "tree-1"
}'

sleep 3

echo "--- VERIFICA CLEANUP ---"
echo "Relay-1 sessions:"
curl -s http://localhost:7071/status | jq '.forwarder.sessions'
echo ""
echo "Egress-1 mountpoints:"
curl -s http://localhost:7073/mountpoints | jq
echo ""
echo "Egress-2 mountpoints:"
curl -s http://localhost:7074/mountpoints | jq

echo ""
echo "--- STOP CONTAINERS ---"
docker-compose -f docker-compose.test.yaml --profile broadcaster down
docker-compose -f docker-compose.test.yaml --profile scale down
docker-compose -f docker-compose.test.yaml down

docker exec redis redis-cli FLUSHALL 2>/dev/null || true

echo ""
echo "TEST COMPLETATO"