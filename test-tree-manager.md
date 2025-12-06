# Tree Manager Test Suite

Guida completa per testare il Tree Manager manualmente.

```bash
cd controller/
docker-compose up --build -d

docker exec redis redis-cli FLUSHALL

# Cleanup container precedenti
docker rm -f $(docker ps -aq --filter "name=test-") 2>/dev/null || true
---

## TEST 1: CREATE TREE (minimal)

### Crea Tree

 
curl -X POST http://localhost:8080/trees \
  -H "Content-Type: application/json" \
  -d '{"tree_id": "test-1", "template": "minimal"}' | jq


**Output atteso:**
json
{
  "tree_id": "test-1",
  "template": "minimal",
  "nodes_count": 3,
  "status": "active",
  "nodes": [
    {
      "NodeId": "test-1-injection-1",
      "NodeType": "injection",
      "Layer": 0,
      ...
    },
    {
      "NodeId": "test-1-relay-1",
      "NodeType": "relay",
      "Layer": 1,
      ...
    },
    {
      "NodeId": "test-1-egress-1",
      "NodeType": "egress",
      "Layer": 2,
      ...
    }
  ]
}

### Lista Container Running

 
docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}" | grep test-1


**Output atteso:**

test-1-injection-1                Up X seconds   0.0.0.0:7070->7070/tcp
test-1-injection-1-janus-vr       Up X seconds   0.0.0.0:8088->8088/tcp, 0.0.0.0:8188->8188/tcp, ...
test-1-relay-1                    Up X seconds   0.0.0.0:7071->7071/tcp
test-1-egress-1                   Up X seconds   0.0.0.0:7072->7073/tcp
test-1-egress-1-janus-streaming   Up X seconds   0.0.0.0:8089->8088/tcp, ...

Devono esserci **5 container** (3 nodi + 2 Janus)
---

## Verifica Redis key

 
docker exec redis redis-cli KEYS "tree:test-1:*" | sort

**Output atteso:**

tree:test-1:children:test-1-injection-1
tree:test-1:children:test-1-relay-1
tree:test-1:controller:node:test-1-egress-1
tree:test-1:controller:node:test-1-injection-1
tree:test-1:controller:node:test-1-relay-1
tree:test-1:egress
tree:test-1:injection
tree:test-1:metadata
tree:test-1:node:test-1-egress-1
tree:test-1:node:test-1-injection-1
tree:test-1:node:test-1-relay-1
tree:test-1:parents:test-1-egress-1
tree:test-1:parents:test-1-relay-1
tree:test-1:relay

### Verifica Metadata

docker exec redis redis-cli HGETALL tree:test-1:metadata

**Output atteso:**

1) "tree_id"
2) "test-1"
3) "template"
4) "minimal"
5) "status"
6) "active"
7) "created_at"
8) "1733344295"
9) "updated_at"
10) "1733344301"

---

## Verifica topologia Redis

### Injection Children

docker exec redis redis-cli SMEMBERS tree:test-1:children:test-1-injection-1

**Output atteso:**

test-1-relay-1

### Relay Parents

docker exec redis redis-cli SMEMBERS tree:test-1:parents:test-1-relay-1

**Output atteso:**

test-1-injection-1

### Relay Children
 
docker exec redis redis-cli SMEMBERS tree:test-1:children:test-1-relay-1

**Output atteso:**

test-1-egress-1

### Egress Parents
 
docker exec redis redis-cli SMEMBERS tree:test-1:parents:test-1-egress-1

**Output atteso:**

test-1-relay-1

### Injection Parents (vuoto)
 
docker exec redis redis-cli SMEMBERS tree:test-1:parents:test-1-injection-1

**Output atteso:**

(empty array)

---

## Verifica topologia 

### Injection Topology

curl -s http://localhost:7070/topology | jq

**Output atteso:**
json
{
  "nodeId": "test-1-injection-1",
  "nodeType": "injection",
  "parents": [],
  "children": [
    "test-1-relay-1"
  ]
}

### Relay Topology

curl -s http://localhost:7071/topology | jq

**Output atteso:**
json
{
  "nodeId": "test-1-relay-1",
  "nodeType": "relay",
  "parents": [
    "test-1-injection-1"
  ],
  "children": [
    "test-1-egress-1"
  ]
}

### Egress Topology

 
curl -s http://localhost:7072/topology | jq

**Output atteso:**
json
{
  "nodeId": "test-1-egress-1",
  "nodeType": "egress",
  "parents": [
    "test-1-relay-1"
  ],
  "children": []
}

---

### Get Single Tree

curl -s http://localhost:8080/trees/test-1 | jq

**Output atteso:**
json
{
  "tree_id": "test-1",
  "template": "minimal",
  "nodes": [...],
  "nodes_count": 3,
  "created_at": "2025-12-04T...",
  "status": "active"
}

### Get Tree Summary

curl -s http://localhost:8080/trees/test-1 | jq '{tree_id, template, nodes_count, status}'

{
  "tree_id": "test-1",
  "template": "minimal",
  "nodes_count": 3,
  "status": "active"
}

---

## List Trees
 
curl -s http://localhost:8080/trees | jq


**Output atteso:**
json
{
  "count": 1,
  "trees": [
    {
      "tree_id": "test-1",
      "template": "minimal",
      "nodes_count": 3,
      "injection_count": 1,
      "relay_count": 1,
      "egress_count": 1,
      "max_layer": 2,
      "status": "active",
      "created_at": "2025-12-04T...",
      "updated_at": "2025-12-04T..."
    }
  ]
}
---

### Crea Tree Small

curl -X POST http://localhost:8080/trees \
  -H "Content-Type: application/json" \
  -d '{"tree_id": "test-2", "template": "small"}' | jq

**Output atteso:**
json
{
  "tree_id": "test-2",
  "template": "small",
  "nodes_count": 5,
  "status": "active",
  "nodes": [...]
}

### Verifica Container

docker ps --filter "name=test-2" --format "{{.Names}}" | wc -l

**Output atteso:**

9
### Verifica Topologia Small

 
# Relay children (dovrebbe avere 3 egress)
docker exec redis redis-cli SMEMBERS tree:test-2:children:test-2-relay-1

**Output atteso:**

test-2-egress-1
test-2-egress-2
test-2-egress-3

---

### Crea Tree Medium

curl -X POST http://localhost:8080/trees \
  -H "Content-Type: application/json" \
  -d '{"tree_id": "test-3", "template": "medium"}' | jq '.nodes_count'

**Output atteso:**

9
### Verifica Distribuzione Round-Robin

 
# Relay-1 children
docker exec redis redis-cli SMEMBERS tree:test-3:children:test-3-relay-1

# Relay-2 children
docker exec redis redis-cli SMEMBERS tree:test-3:children:test-3-relay-2


**Output atteso:**

# Relay-1: 3 egress
test-3-egress-1
test-3-egress-2
test-3-egress-3

# Relay-2: 3 egress
test-3-egress-4
test-3-egress-5
test-3-egress-6

---

## Verifica porte

### Porte API

 
docker ps --filter "name=test-" --format "{{.Ports}}" | \
  grep -oE '[0-9]+->70[0-9]+/tcp' | \
  sort


**Output atteso (esempio):**

7070->7070/tcp
7071->7071/tcp
7072->7073/tcp
7073->7073/tcp
7074->7073/tcp
7075->7071/tcp
7076->7073/tcp
7077->7073/tcp
7078->7073/tcp

### Porte Janus HTTP

 
docker ps --filter "name=janus" --format "{{.Ports}}" | \
  grep -oE '[0-9]+->8088/tcp' | \
  sort

### Range WebRTC

 
docker ps --filter "name=janus" --format "{{.Ports}}" | \
  grep -oE '[0-9]+-[0-9]+' | \
  sort | uniq


**Output atteso (esempio):**

20000-20099:
20100-20199:
20200-20299:
20300-20399:
20400-20499:
20500-20599:
20600-20699:

---

### Distruggi test-1

 
curl -X DELETE http://localhost:8080/trees/test-1 | jq


**Output atteso:**
json
{
  "status": "destroyed",
  "tree_id": "test-1"
}


### Verifica Cleanup Container

 
# Attendi 5 secondi
sleep 5

# Verifica
docker ps --filter "name=test-1" --format "{{.Names}}"


**Output atteso:**

(vuoto)

### Verifica Cleanup Redis

 
docker exec redis redis-cli KEYS "tree:test-1:*"


**Output atteso:**

(empty array)

---

### Distruggi test-2

 
curl -X DELETE http://localhost:8080/trees/test-2 | jq
sleep 5
docker ps --filter "name=test-2" --format "{{.Names}}"

### Distruggi test-3

 
curl -X DELETE http://localhost:8080/trees/test-3 | jq
sleep 5
docker ps --filter "name=test-3" --format "{{.Names}}"

### Verifica Cleanup Finale

 
# Nessun test container
docker ps --filter "name=test-" --format "{{.Names}}"

# Nessuna test key
docker exec redis redis-cli KEYS "tree:test-*"


**Output atteso per entrambi:**

(vuoto)
---

### 14.1 Tree Già Esistente

 
# Crea tree
curl -X POST http://localhost:8080/trees \
  -H "Content-Type: application/json" \
  -d '{"tree_id": "test-duplicate", "template": "minimal"}' | jq

# Prova a ricreare stesso ID
curl -X POST http://localhost:8080/trees \
  -H "Content-Type: application/json" \
  -d '{"tree_id": "test-duplicate", "template": "minimal"}' | jq

 
# Distruggi
curl -X DELETE http://localhost:8080/trees/test-duplicate | jq

**Output atteso (2° richiesta):**
json
{
  "error": "tree duplicate already exists"
}

### Template Non Esistente

 
curl -X POST http://localhost:8080/trees \
  -H "Content-Type: application/json" \
  -d '{"tree_id": "invalid", "template": "nonexistent"}' | jq


**Output atteso:**
json
{
  "error": "invalid template: template not found: nonexistent"
}

### Get Tree Non Esistente

 
curl -s http://localhost:8080/trees/nonexistent | jq


**Output atteso:**
json
{
  "error": "tree not found: ..."
}
---

## COMANDI UTILI

 
# Cleanup totale
docker rm -f $(docker ps -aq --filter "name=test-")
docker exec redis redis-cli FLUSHALL

# Verifica porte allocate
netstat -tuln | grep LISTEN | grep -E "7070|8088|8188|20000"

# Logs controller
docker logs -f controller

# Logs nodo specifico
docker logs -f test-1-injection-1

# Redis monitor
docker exec redis redis-cli MONITOR

# Conta container per tree
docker ps --filter "name=test-1" --format "{{.Names}}" | wc -l
```
**Fine Test Suite** 