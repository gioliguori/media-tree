#!/bin/bash

# Funzione per pausa
pause() {
    echo ""
    echo ">>> Premi INVIO per continuare..."
    read
}

echo "=========================================="
echo "TEST TOPOLOGY CHANGE"
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
docker-compose -f docker-compose.test.yaml up -d injection-1 relay-1 relay-2 egress-1
sleep 10

echo "--- VERIFICA NODI ---"
curl http://localhost:7070/status | jq '{nodeId, nodeType, healthy}'
curl http://localhost:7071/status | jq '{nodeId, nodeType, healthy}'
curl http://localhost:7072/status | jq '{nodeId, nodeType, healthy}'
curl http://localhost:7073/status | jq '{nodeId, nodeType, healthy}'

echo "--- CONFIGURAZIONE TOPOLOGIA: injection-1 -> relay-1 ---"
docker exec redis redis-cli SADD children:injection-1 relay-1
docker exec redis redis-cli SET parent:relay-1 injection-1

docker exec redis redis-cli PUBLISH topology:injection-1 '{"type":"child-added","nodeId":"injection-1","childId":"relay-1"}'
docker exec redis redis-cli PUBLISH topology:relay-1 '{"type":"parent-changed","nodeId":"relay-1","newParent":"injection-1"}'

echo "--- CONFIGURAZIONE TOPOLOGIA: relay-1 -> relay-2 ---"
docker exec redis redis-cli SADD children:relay-1 relay-2
docker exec redis redis-cli SET parent:relay-2 relay-1

docker exec redis redis-cli PUBLISH topology:relay-1 '{"type":"child-added","nodeId":"relay-1","childId":"relay-2"}'
docker exec redis redis-cli PUBLISH topology:relay-2 '{"type":"parent-changed","nodeId":"relay-2","newParent":"relay-1"}'

echo "--- CONFIGURAZIONE TOPOLOGIA: relay-2 -> egress-1 ---"
docker exec redis redis-cli SADD children:relay-2 egress-1
docker exec redis redis-cli SET parent:egress-1 relay-2

docker exec redis redis-cli PUBLISH topology:relay-2 '{"type":"child-added","nodeId":"relay-2","childId":"egress-1"}'
docker exec redis redis-cli PUBLISH topology:egress-1 '{"type":"parent-changed","nodeId":"egress-1","newParent":"relay-2"}'

echo "--- VERIFICA TOPOLOGIA COMPLETA ---"
echo "injection-1:"
curl http://localhost:7070/topology | jq '.children'
echo ""
echo "relay-1:"
curl http://localhost:7071/topology | jq
echo ""
echo "relay-2:"
curl http://localhost:7072/topology | jq
echo ""
echo "egress-1:"
curl http://localhost:7073/topology | jq '.parent'

pause

echo "--- CREA SESSIONE test-chain ---"
curl -X POST http://localhost:7070/session \
  -H "Content-Type: application/json" \
  -d '{"sessionId": "test-chain", "roomId": 3001, "audioSsrc": 5555, "videoSsrc": 6666}' | jq

echo "--- PUBBLICA EVENTO session-created ---"
docker exec redis redis-cli PUBLISH sessions:tree:tree-1 '{"type":"session-created","sessionId":"test-chain","treeId":"tree-1"}'
sleep 2

echo "--- VERIFICA MOUNTPOINT egress-1 ---"
curl http://localhost:7073/mountpoint/test-chain | jq
curl http://localhost:7070/sessions | jq
curl http://localhost:7073/mountpoints | jq

pause

echo "--- START BROADCASTER ---"
docker-compose -f docker-compose.test.yaml --profile test-chain up -d
sleep 10

echo "--- APRI VIEWER ---"
echo "http://localhost:7073/?id=test-chain"

pause

echo ""
echo "=========================================="
echo "TOPOLOGY CHANGE: Rimuovi relay-2"
echo "=========================================="
echo ""
echo "PRIMA: injection-1 -> relay-1 -> relay-2 -> egress-1"
echo "DOPO:  injection-1 -> relay-1 -> egress-1"
echo ""

echo "--- RIMUOVI relay-2 dalla topologia ---"
docker exec redis redis-cli SREM children:relay-1 relay-2
docker exec redis redis-cli DEL parent:relay-2

docker exec redis redis-cli PUBLISH topology:relay-1 '{"type":"child-removed","nodeId":"relay-1","childId":"relay-2"}'
docker exec redis redis-cli PUBLISH topology:relay-2 '{"type":"parent-changed","nodeId":"relay-2","newParent":null}'

echo "--- COLLEGA relay-1 direttamente a egress-1 ---"
docker exec redis redis-cli SADD children:relay-1 egress-1
docker exec redis redis-cli SET parent:egress-1 relay-1

docker exec redis redis-cli PUBLISH topology:relay-1 '{"type":"child-added","nodeId":"relay-1","childId":"egress-1"}'
docker exec redis redis-cli PUBLISH topology:egress-1 '{"type":"parent-changed","nodeId":"egress-1","newParent":"relay-1"}'

echo "--- VERIFICA TOPOLOGIA AGGIORNATA ---"
echo "relay-1 (dovrebbe avere egress-1 come child):"
curl http://localhost:7071/topology | jq
echo ""
echo "egress-1 (parent dovrebbe essere relay-1):"
curl http://localhost:7073/topology | jq '.parent'
echo ""
echo "relay-2 (dovrebbe essere isolato):"
curl http://localhost:7072/topology | jq

echo "--- RICARICA VIEWER ---"
echo "http://localhost:7073/?id=test-chain"

pause

echo "--- VERIFICA RTP egress-1 (dovrebbe ricevere da relay-1) ---"
docker exec egress-1 apt-get update -qq && docker exec egress-1 apt-get install -y tcpdump
docker exec egress-1 timeout 5 tcpdump -i eth0 -n 'udp port 5002' -c 10 2>&1 | tail -1

pause

echo "--- VERIFICA RTP relay-2 (NON dovrebbe ricevere) ---"
docker exec relay-2 apt-get update -qq && docker exec relay-2 apt-get install -y tcpdump
docker exec relay-2 timeout 3 tcpdump -i eth0 -n 'udp port 5002' -c 10 2>&1 | tail -1

pause

echo ""
echo "=========================================="
echo "CLEANUP"
echo "=========================================="
echo ""

curl -X POST http://localhost:7070/session/test-chain/destroy

docker exec redis redis-cli PUBLISH sessions:tree:tree-1 '{"type":"session-destroyed","sessionId":"test-chain","treeId":"tree-1"}'
sleep 2

docker exec redis redis-cli FLUSHALL
docker-compose -f docker-compose.test.yaml --profile test-chain down
docker-compose -f docker-compose.test.yaml down

echo ""
echo "TEST COMPLETATO!"