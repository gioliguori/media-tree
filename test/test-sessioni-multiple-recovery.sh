#!/bin/bash

# Funzione per pausa
pause() {
    echo ""
    echo ">>> Premi INVIO per continuare..."
    read
}

echo "=========================================="
echo "TEST MULTI-SESSION + RECOVERY"
echo "=========================================="
echo ""

echo "--- CLEANUP INIZIALE ---"
docker-compose -f docker-compose.test.yaml down
docker-compose -f docker-compose.test.yaml --profile broadcaster down
docker-compose -f docker-compose.test.yaml --profile test-chain down
docker-compose -f docker-compose.test.yaml --profile test-scale down
docker-compose -f docker-compose.test.yaml --profile scale down

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
curl http://localhost:7070/status | jq '{nodeId, nodeType, healthy}'
curl http://localhost:7071/status | jq '{nodeId, nodeType, healthy}'
curl http://localhost:7073/status | jq '{nodeId, nodeType, healthy}'

echo "--- CONFIGURAZIONE TOPOLOGIA: injection-1 -> relay-1 ---"
docker exec redis redis-cli SADD children:injection-1 relay-1
docker exec redis redis-cli SET parent:relay-1 injection-1

docker exec redis redis-cli PUBLISH topology:injection-1 '{"type":"child-added","nodeId":"injection-1","childId":"relay-1"}'
docker exec redis redis-cli PUBLISH topology:relay-1 '{"type":"parent-changed","nodeId":"relay-1","newParent":"injection-1"}'

echo "--- CONFIGURAZIONE TOPOLOGIA: relay-1 -> egress-1 ---"
docker exec redis redis-cli SADD children:relay-1 egress-1
docker exec redis redis-cli SET parent:egress-1 relay-1

docker exec redis redis-cli PUBLISH topology:relay-1 '{"type":"child-added","nodeId":"relay-1","childId":"egress-1"}'
docker exec redis redis-cli PUBLISH topology:egress-1 '{"type":"parent-changed","nodeId":"egress-1","newParent":"relay-1"}'

echo "--- VERIFICA TOPOLOGIA ---"
curl http://localhost:7070/topology | jq '.children'
curl http://localhost:7071/topology | jq
curl http://localhost:7073/topology | jq '.parent'

pause

echo ""
echo "=========================================="
echo "SESSIONE 1: broadcaster-1"
echo "=========================================="
echo ""

echo "--- CREA SESSIONE broadcaster-1 ---"
curl -X POST http://localhost:7070/session \
  -H "Content-Type: application/json" \
  -d '{"sessionId": "broadcaster-1", "roomId": 2001, "audioSsrc": 1111, "videoSsrc": 2222}' | jq

echo "--- PUBBLICA EVENTO session-created ---"
docker exec redis redis-cli PUBLISH sessions:tree:injection-1 '{"type":"session-created","sessionId":"broadcaster-1","treeId":"injection-1"}'
sleep 2

echo "--- VERIFICA MOUNTPOINT broadcaster-1 ---"
curl http://localhost:7073/mountpoint/broadcaster-1 | jq

pause

echo ""
echo "=========================================="
echo "SESSIONE 2: broadcaster-2"
echo "=========================================="
echo ""

echo "--- CREA SESSIONE broadcaster-2 ---"
curl -X POST http://localhost:7070/session \
  -H "Content-Type: application/json" \
  -d '{"sessionId": "broadcaster-2", "roomId": 2002, "audioSsrc": 3333, "videoSsrc": 4444}' | jq

echo "--- PUBBLICA EVENTO session-created ---"
docker exec redis redis-cli PUBLISH sessions:tree:injection-1 '{"type":"session-created","sessionId":"broadcaster-2","treeId":"injection-1"}'
sleep 2

echo "--- VERIFICA MOUNTPOINT broadcaster-2 ---"
curl http://localhost:7073/mountpoint/broadcaster-2 | jq

echo "--- VERIFICA TUTTE LE SESSIONI E MOUNTPOINT ---"
curl http://localhost:7070/sessions | jq
curl http://localhost:7073/mountpoints | jq

pause
echo "--- START BROADCASTERS ---"
docker-compose -f docker-compose.test.yaml --profile broadcaster up -d
sleep 10

echo "--- APRI VIEWER broadcaster-1 ---"
echo "http://localhost:7073/?id=broadcaster-1"

echo "--- APRI VIEWER broadcaster-2 ---"
echo "http://localhost:7073/?id=broadcaster-2"

pause

echo ""
echo "=========================================="
echo "TEST RECOVERY: Injection Node"
echo "=========================================="
echo ""

echo "--- RESTART injection-1 ---"
docker-compose -f docker-compose.test.yaml restart injection-1
sleep 15

echo "--- VERIFICA RECOVERY injection-1 ---"
curl http://localhost:7070/status | jq '{nodeId, healthy, sessions}'
curl http://localhost:7070/sessions | jq

echo "--- RIAVVIA BROADCASTERS (riconnessione) ---"
docker-compose -f docker-compose.test.yaml --profile broadcaster up -d
sleep 5

echo "--- RICARICA VIEWER broadcaster-1 ---"
echo "http://localhost:7073/?id=broadcaster-1"

echo "--- RICARICA VIEWER broadcaster-2 ---"
echo "http://localhost:7073/?id=broadcaster-2"

pause

echo ""
echo "=========================================="
echo "TEST RECOVERY: Relay Node"
echo "=========================================="
echo ""

echo "--- RESTART relay-1 ---"
docker-compose -f docker-compose.test.yaml restart relay-1
sleep 15

echo "--- VERIFICA RECOVERY relay-1 ---"
curl http://localhost:7071/status | jq '{nodeId, healthy, forwarder}'

echo "--- Stream dovrebbe essere visibile dopo qualche secondo ---"
echo "http://localhost:7073/?id=broadcaster-1"
echo "http://localhost:7073/?id=broadcaster-2"

pause

echo ""
echo "=========================================="
echo "TEST RECOVERY: Egress Node"
echo "=========================================="
echo ""

echo "--- RESTART egress-1 ---"
docker-compose -f docker-compose.test.yaml restart egress-1
sleep 15

echo "--- VERIFICA RECOVERY egress-1 ---"
curl http://localhost:7073/status | jq '{nodeId, healthy, mountpoints, forwarder}'
curl http://localhost:7073/mountpoints | jq

echo "--- RICARICA VIEWER broadcaster-1 ---"
echo "http://localhost:7073/?id=broadcaster-1"

echo "--- RICARICA VIEWER broadcaster-2 ---"
echo "http://localhost:7073/?id=broadcaster-2"

pause

echo ""
echo "=========================================="
echo "TEST CRASH PROCESSO C"
echo "=========================================="
echo ""

echo "Esegui manualmente:"
echo "  docker exec -it egress-1 sh"
echo "  ps aux | grep egress-forwarder"
echo "  kill -9 <PID>"
echo ""
echo "Poi ricarica i viewer per verificare che il forwarder riparte"

pause 

echo ""
echo "=========================================="
echo "CLEANUP"
echo "=========================================="
echo ""

curl -X POST http://localhost:7070/session/broadcaster-1/destroy
curl -X POST http://localhost:7070/session/broadcaster-2/destroy

docker exec redis redis-cli PUBLISH sessions:tree:injection-1 '{"type":"session-destroyed","sessionId":"broadcaster-1","treeId":"injection-1"}'
docker exec redis redis-cli PUBLISH sessions:tree:injection-1 '{"type":"session-destroyed","sessionId":"broadcaster-2","treeId":"injection-1"}'
sleep 2

docker exec redis redis-cli FLUSHALL
docker-compose -f docker-compose.test.yaml --profile broadcaster down
docker-compose -f docker-compose.test.yaml down

echo ""
echo "TEST COMPLETATO!"