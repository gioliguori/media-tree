#!/bin/bash

# Funzione per pausa
pause() {
    echo ""
    echo ">>> Premi INVIO per continuare..."
    read
}

echo "=========================================="
echo "TEST SCALE-OUT / SCALE-DOWN"
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
docker-compose -f docker-compose.test.yaml up -d redis janus-videoroom janus-streaming-1 janus-streaming-2
sleep 5
docker exec redis redis-cli FLUSHALL

echo "--- START NODI (solo egress-1) ---"
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

echo "--- CREA SESSIONE test-scale ---"
curl -X POST http://localhost:7070/session \
  -H "Content-Type: application/json" \
  -d '{"sessionId": "test-scale", "roomId": 4001, "audioSsrc": 7777, "videoSsrc": 8888}' | jq

pause

echo "--- PUBBLICA EVENTO session-created ---"
docker exec redis redis-cli PUBLISH sessions:tree:tree-1 '{"type":"session-created","sessionId":"test-scale","treeId":"tree-1"}'
sleep 2

echo "--- VERIFICA MOUNTPOINT su egress-1 ---"
curl http://localhost:7073/mountpoint/test-scale | jq
curl http://localhost:7070/sessions | jq

pause

echo "--- START BROADCASTER ---"
docker-compose -f docker-compose.test.yaml --profile test-scale up -d
sleep 10

echo "--- APRI VIEWER egress-1 ---"
echo "http://localhost:7073/?id=test-scale"

pause

echo ""
echo "=========================================="
echo "SCALE-OUT: Aggiungi egress-2"
echo "=========================================="
echo ""

echo "--- START egress-2 ---"
docker-compose -f docker-compose.test.yaml up -d egress-2
sleep 10

echo "--- VERIFICA egress-2 ---"
curl http://localhost:7074/status | jq '{nodeId, nodeType, healthy}'

pause

echo "--- MOUNTPOINT egress-2 (creato automaticamente) ---"
curl http://localhost:7074/mountpoint/test-scale | jq

pause

echo "--- AGGIUNGI egress-2 ALLA TOPOLOGIA ---"
docker exec redis redis-cli SADD children:relay-1 egress-2
docker exec redis redis-cli SET parent:egress-2 relay-1

docker exec redis redis-cli PUBLISH topology:relay-1 '{"type":"child-added","nodeId":"relay-1","childId":"egress-2"}'
docker exec redis redis-cli PUBLISH topology:egress-2 '{"type":"parent-changed","nodeId":"egress-2","newParent":"relay-1"}'
sleep 2

echo "--- VERIFICA TOPOLOGIA AGGIORNATA ---"
curl http://localhost:7071/topology | jq '.children'
curl http://localhost:7074/topology | jq '.parent'

pause

echo "--- APRI VIEWER egress-2 ---"
echo "http://localhost:7074/?id=test-scale"

pause

echo ""
echo "=========================================="
echo "SCALE-DOWN: Rimuovi egress-1"
echo "=========================================="
echo ""

echo "--- RIMUOVI egress-1 DALLA TOPOLOGIA ---"
docker exec redis redis-cli SREM children:relay-1 egress-1
docker exec redis redis-cli DEL parent:egress-1

docker exec redis redis-cli PUBLISH topology:relay-1 '{"type":"child-removed","nodeId":"relay-1","childId":"egress-1"}'
docker exec redis redis-cli PUBLISH topology:egress-1 '{"type":"parent-changed","nodeId":"egress-1","newParent":null}'

echo "--- VERIFICA TOPOLOGIA ---"
curl http://localhost:7071/topology | jq '.children'
curl http://localhost:7073/topology | jq '.parent'

pause

echo "--- DISTRUGGI MOUNTPOINT egress-1 ---"
docker exec redis redis-cli PUBLISH sessions:node:egress-1 '{"type":"session-destroyed","sessionId":"test-scale","treeId":"tree-1"}'
sleep 2

pause

echo "--- VERIFICA RTP egress-1 (non dovrebbe ricevere) ---"
docker exec egress-1 apt-get update -qq && docker exec egress-1 apt-get install -y tcpdump
docker exec egress-1 timeout 3 tcpdump -i eth0 -n 'udp port 5002' -c 10 2>&1 | tail -1

pause

echo "--- VERIFICA RTP egress-2 (dovrebbe ancora ricevere) ---"
docker exec egress-2 apt-get update -qq && docker exec egress-2 apt-get install -y tcpdump
docker exec egress-2 timeout 5 tcpdump -i eth0 -n 'udp port 5002' -c 10 2>&1 | tail -1

pause

echo "--- APRI VIEWER egress-2 (dovrebbe funzionare) ---"
echo "http://localhost:7074/?id=test-scale"

pause

echo ""
echo "=========================================="
echo "CLEANUP"
echo "=========================================="
echo ""



curl -X POST http://localhost:7070/session/test-scale/destroy

docker exec redis redis-cli PUBLISH sessions:tree:tree-1 '{"type":"session-destroyed","sessionId":"test-scale","treeId":"tree-1"}'
echo ""
sleep 2

docker exec redis redis-cli FLUSHALL
docker-compose -f docker-compose.test.yaml --profile test-scale down
docker-compose -f docker-compose.test.yaml --profile scale down
docker-compose -f docker-compose.test.yaml down

echo ""
echo "TEST COMPLETATO!"