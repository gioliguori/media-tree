Test tree and session

```bash
cd controller/
docker-compose up --build -d

# Reset Redis per partire puliti
docker exec redis redis-cli FLUSHALL

# Rimuovi eventuali container rimasti da test precedenti
docker rm -f $(docker ps -aq --filter "name=test-") 2>/dev/null || true

PART 1: Tree Manager (Infrastruttura)
Create Tree "Minimal"


curl -X POST http://localhost:8080/api/trees \
  -H "Content-Type: application/json" \
  -d '{"tree_id": "test-1", "template": "minimal"}' | jq '.'

Output Atteso:
JSON
{
  "tree_id": "test-1",
  "template": "minimal",
  "nodes_count": 3,
  "status": "active",
  "nodes": [ ... ]
}

Verifica Container Running
Devono esserci 5 container (3 nodi logici + 2 sidecar Janus).

docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}" | grep test-1

Output Atteso:
test-1-injection-1 (Porta 7070)
test-1-injection-1-janus-vr (Porta 8088/8188)
test-1-relay-1 (Porta 7071)
test-1-egress-1 (Porta 7072)
test-1-egress-1-janus-streaming (Porta 8089)

Verifica Deep Topologia
Verifica che il controller abbia mappato correttamente le relazioni padre-figlio nel database.

Lista Chiavi
docker exec redis redis-cli KEYS "tree:test-1:*" | sort
Output Atteso: Deve contenere chiavi per children, parents, metadata, node, controller:node.

Verifica Relazioni Gerarchiche

Injection Children
docker exec redis redis-cli SMEMBERS tree:test-1:children:test-1-injection-1
Atteso: test-1-relay-1

Relay Parents
docker exec redis redis-cli SMEMBERS tree:test-1:parents:test-1-relay-1
Atteso: test-1-injection-1

Relay Children
docker exec redis redis-cli SMEMBERS tree:test-1:children:test-1-relay-1
Atteso: test-1-egress-1

Egress Parents
docker exec redis redis-cli SMEMBERS tree:test-1:parents:test-1-egress-1
Atteso: test-1-relay-1

Verifica API Interna Nodi
Interroghiamo direttamente i nodi via HTTP per assicurarci che abbiano ricevuto la configurazione.

Injection Node Topology:
curl -s http://localhost:7070/topology | jq
Atteso: children contiene test-1-relay-1, parents vuoto.

Relay Node Topology:
curl -s http://localhost:7071/topology | jq
Atteso: parents contiene test-1-injection-1, children contiene test-1-egress-1.

Egress Node Topology:
curl -s http://localhost:7072/topology | jq
Atteso: parents contiene test-1-relay-1, children vuoto.

Test Template Complessi & Scaling
Verifica allocazione delle risorse per template più grandi

Distruzione
curl -X DELETE http://localhost:8080/api/trees/test-1 | jq '.'
sleep 5
# Deve essere vuoto
docker ps --filter "name=test-1" --format "{{.Names}}"
# Deve essere vuoto
docker exec redis redis-cli KEYS "tree:test-1:*"

Template "Small"
curl -X POST http://localhost:8080/api/trees \
  -H "Content-Type: application/json" \
  -d '{"tree_id": "test-2", "template": "small"}' | jq '.nodes_count'
Output Atteso: 5

Verifica che il Relay abbia 3 figli:
docker exec redis redis-cli SMEMBERS tree:test-2:children:test-2-relay-1
Atteso: 3 nodi egress distinti.

Distruzione
curl -X DELETE http://localhost:8080/api/trees/test-2 | jq '.'

Template "Medium"
curl -X POST http://localhost:8080/api/trees \
  -H "Content-Type: application/json" \
  -d '{"tree_id": "test-3", "template": "medium"}' | jq '.nodes_count'

Output Atteso: 9

Verifica bilanciamento nodi Egress sui Relay:
docker exec redis redis-cli SMEMBERS tree:test-3:children:test-3-relay-1
docker exec redis redis-cli SMEMBERS tree:test-3:children:test-3-relay-2
Atteso: 3 Egress sul Relay-1 e 3 Egress sul Relay-2.

Verifica Allocazione Porte
Controllo che non ci siano conflitti e che i range siano rispettati.

Porte API (Range 7070+):
docker ps --filter "name=test-" --format "{{.Ports}}" | grep -oE '[0-9]+->70[0-9]+/tcp' | sort

Porte WebRTC (Range 20000+ per Janus):
docker ps --filter "name=janus" --format "{{.Ports}}" | grep -oE '[0-9]+-[0-9]+' | sort | uniq

PART 2: Session Manager
Test creazione automatica e manuale

Creazione Automatica
curl -X POST http://localhost:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{
    "session_id": "broadcaster-auto",
    "scale": "low"
  }' | jq '.'

Round Robin
Creiamo sessioni multiple per verificare che vengano distribuite su nodi di injection diversi.
# Sessione U1
curl -X POST http://localhost:8080/api/sessions -d '{"session_id": "u1", "scale": "low"}' -H "Content-Type: application/json" | jq '.injection_node_id'
# Sessione U2
curl -X POST http://localhost:8080/api/sessions -d '{"session_id": "u2", "scale": "low"}' -H "Content-Type: application/json" | jq '.injection_node_id'
Atteso: u1 e u2 dovrebbero trovarsi su injection nodes diversi (es. test-3-injection-1 e test-3-injection-2).

Creazione Manuale
Verifichiamo che la modalità manuale funzioni ancora.

curl -X POST http://localhost:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{
    "session_id": "broadcaster-manual",
    "tree_id": "test-3",
    "injection_node_id": "test-3-injection-1",
    "egress_node_ids": ["test-3-egress-1"]
  }' | jq '.'
Atteso: Sessione creata esattamente sui nodi richiesti.

Get Session
Verifica lettura dettagliata.
curl -X GET http://localhost:8080/api/trees/test-3/sessions/broadcaster-auto | jq '.'

List Session
Verifica lettura sessioni per albero
curl -X GET http://localhost:8080/api/trees/test-3/sessions| jq '.'
Atteso: JSON completo delle sessioni

PART 3: Distruzione e Cleanup
Verifica che il sistema rilasci correttamente le risorse.

Cancellazione Sessione
curl -X DELETE http://localhost:8080/api/trees/test-3/sessions/broadcaster-auto | jq '.'

Verifica Cleanup Redis per Sessione:
# Metadati (Deve essere vuoto)
docker exec redis redis-cli HGETALL tree:test-3:session:broadcaster-auto
# Routing (Deve essere vuoto)
docker exec redis redis-cli KEYS "tree:test-3:routing:broadcaster-auto:*"

Distruzione Tree

curl -X DELETE http://localhost:8080/api/trees/test-1 | jq
curl -X DELETE http://localhost:8080/api/trees/test-2 | jq
curl -X DELETE http://localhost:8080/api/trees/test-3 | jq


sleep 5
docker ps --filter "name=test-" --format "{{.Names}}"
Verifica Cleanup Redis Totale:


docker exec redis redis-cli KEYS "tree:test-*"
Output Atteso: (empty array)