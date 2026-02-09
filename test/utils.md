Test Routing

## Setup Ambiente

cd controller
docker compose up --build -d


## Monitoraggio Real-Time
Su secondo terminale

watch -n 1 "echo '--- GLOBAL RELAY LOAD (0-20) ---'; \
docker exec redis redis-cli ZRANGE pool:relay:load 0 -1 WITHSCORES; \
echo ''; echo '--- ACTIVE CHAINS ---'; \
docker exec redis redis-cli KEYS 'session:*:chain' | xargs -I {} sh -c \"echo '{}:'; docker exec redis redis-cli LRANGE {} 0 -1 | tr '\n' ' ' ; echo ''\"; \
echo ''; echo '--- EDGE COUNTS ---'; \
docker exec redis redis-cli KEYS 'session:*:edge_counts' | xargs -I {} sh -c \"echo '{}:'; docker exec redis redis-cli HGETALL {}\""


## Creazione Sessione e Ingestione
Creare la prima sessione e verificare che la catena sia inizializzata correttamente.
cd ../test
./create-session.sh "test-mesh"

Verifica Redis:
session:test-mesh:chain deve contenere solo relay-root-1.

## Concorrenza
Creare una seconda sessione per verificare se il sistema aggrega i flussi sullo stesso nodo fisico (Fill-First).

./create-session.sh "test-mesh-2"

# Chiedere un viewer per ognuna
curl http://localhost:8080/api/sessions/test-mesh/view
curl http://localhost:8080/api/sessions/test-mesh-2/view
Verifica Redis:

relay-1 deve comparire in entrambe le catene.

pool:relay:load per relay-1 deve essere 4 (1 riserva + 1 edge per sessione A, 1 riserva + 1 edge per sessione B).

## Saturazione e Deepening (Catena Lunga)
Simulare la saturazione fisica di un nodo e l'espansione della mesh.

## Satura relay-1 a mano
docker exec redis redis-cli ZADD pool:relay:load 20 relay-1

## Crea un nuovo relay standalone via API
curl -X POST "http://localhost:8080/api/nodes?type=relay"

## Satura l'attuale Egress aprendo 8 tab del browser
## Attendi scaling egress
## Richiedi un nuovo viewer (9°) per forzare uno sdoppiamento del path
curl http://localhost:8080/api/sessions/test-mesh/view

Verifica logica:

Il sistema vede relay-1 pieno.

Crea un nuovo egress-2.

Allunga la catena: test-mesh:chain -> [relay-root-1, relay-1, relay-2].

relay-1 inoltra il flusso a relay-2.

## Backtracking
Scollegare i viewer dall'ultimo anello della catena per testare il pruning.

Verifica Redis:

relay-2 deve avere edge_counts = 0.

Il Controller deve rimuovere relay-2 dalla catena di test-mesh.

pool:relay:load di relay-2 deve tornare a 0 (se non ha altre sessioni).

La sessione test-mesh-2 su relay-1 deve continuare a funzionare regolarmente.

## Cleanup Totale
Chiudere sessione da parte del broadcaster. (whip client)