#!/bin/bash

# Script per testare la distruzione completa di sessioni
# Testa sia la distruzione selettiva di path che la distruzione completa
# Verifica che tutto venga pulito correttamente da Redis

#set -e

echo "=================================================="
echo "SESSION TEST"
echo "=================================================="
echo ""

echo "Setup Iniziale"
# Avvio tutti i servizi docker
echo "Avvio tutti i servizi..."
cd ../controller
docker-compose up --build -d
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "=================================================="
echo "CREAZIONE ALBERO E SESSIONE"
echo "=================================================="
echo ""

# Creo un albero small per i test
#echo "Creo Tree default-tree..."
#curl -X POST http://localhost:8080/api/trees \
#  -H "Content-Type: application/json" \
#  -d '{"treeId":"default-tree","template":"small"}' | jq
#echo ""
#read -p "Premi invio per continuare..."
#
echo ""
# Creo una sessione per il broadcaster
echo "Creo Session broadcaster-1..."
curl -X POST http://localhost:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{"sessionId":"broadcaster-1"}' | jq
echo ""
read -p "Premi invio per continuare..."

echo ""
# Verifico che i metadati della sessione siano salvati correttamente in Redis
echo "Verifico Session Metadata in Redis"
echo "Session metadata:"
docker exec redis redis-cli HGETALL tree:default-tree:session:broadcaster-1
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Session index:"
docker exec redis redis-cli SMEMBERS tree:default-tree:sessions
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Injection ha la session:"
docker exec redis redis-cli SMEMBERS tree:default-tree:node:injection-1:sessions
echo ""
echo "Session dormiente creata correttamente"
read -p "Premi invio per continuare..."

echo ""
echo "=================================================="
echo "PROVISIONING VIEWER - Browser"
echo "=================================================="
echo ""

# Apro un browser per simulare un viewer che si connette
echo "AZIONE MANUALE: Apri browser -> http://localhost:8080/"
read -p "Premi invio dopo aver aperto il browser 1..."

echo ""
# Verifico che il path per egress-1 sia stato creato correttamente
echo "Verifico Path 1 Creato - Redis"
echo "Lista egress:"
docker exec redis redis-cli SMEMBERS tree:default-tree:session:broadcaster-1:egresses
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Path salvato:"
docker exec redis redis-cli GET tree:default-tree:session:broadcaster-1:path:egress-1
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Relay-root ha la session:"
docker exec redis redis-cli SMEMBERS tree:default-tree:node:relay-root-1:sessions
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Egress-1 ha la session:"
docker exec redis redis-cli SMEMBERS tree:default-tree:node:egress-1:sessions
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Routing table relay-root -> egress-1:"
docker exec redis redis-cli SMEMBERS tree:default-tree:routing:broadcaster-1:relay-root-1
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Mountpoint egress-1:"
docker exec redis redis-cli HGETALL tree:default-tree:mountpoint:egress-1:broadcaster-1
echo ""
echo "Path 1 completo"
read -p "Premi invio per continuare..."

echo ""
echo "=================================================="
echo "VIEWER 2"
echo "=================================================="
echo ""

# Apro un secondo browser per testare il path balancing
echo "AZIONE MANUALE: Apri browser -> http://localhost:8080/"
read -p "Premi invio dopo aver aperto il browser 2..."

echo ""
echo "Verifico Path 2 Creato"
echo "Lista egress - dovrebbero essere 2:"
docker exec redis redis-cli SMEMBERS tree:default-tree:session:broadcaster-1:egresses
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Path 2 salvato:"
docker exec redis redis-cli GET tree:default-tree:session:broadcaster-1:path:egress-2
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Egress-2 ha la session:"
docker exec redis redis-cli SMEMBERS tree:default-tree:node:egress-2:sessions
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Routing table aggiornata - relay-root -> egress-1 ed egress-2:"
docker exec redis redis-cli SMEMBERS tree:default-tree:routing:broadcaster-1:relay-root-1
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Mountpoint egress-2:"
docker exec redis redis-cli HGETALL tree:default-tree:mountpoint:egress-2:broadcaster-1
echo ""
echo "Path 2 completo - 2 path attivi"
read -p "Premi invio per continuare..."

echo ""
echo "=================================================="
echo "INVIO FLUSSO - WHIP Client"
echo "=================================================="
echo ""

# Avvio il client WHIP per inviare il flusso video all'injection node
echo "Avvio WHIP Client..."
docker-compose up -d whip-client-1
echo ""
echo "Client invia stream a: http://injection-1:7070/whip/broadcaster-1"
read -p "Premi invio per continuare..."

echo ""
echo "Verifica Flusso su Egress"
echo "Browser 1 - egress-1 - localhost:7072 - video/audio dovrebbe funzionare (pallina)"
echo "Browser 2 - egress-2 - localhost:7073 - video/audio dovrebbe funzionare (pallina)"
read -p "Premi invio per continuare..."

echo ""
echo "=================================================="
echo "DISTRUZIONE SELETTIVA PATH"
echo "=================================================="
echo ""

# Distruggo solo il path per egress-2, lasciando egress-1 attivo
# Questo testa che il cleanup sia selettivo e non cancelli tutto
echo "Distruggo Path egress-2..."
curl -X DELETE http://localhost:8080/api/trees/default-tree/sessions/broadcaster-1/egress/egress-2
echo ""
echo ""
read -p "Premi invio per continuare..."

echo ""
# Verifico che solo il path egress-2 sia stato rimosso
# egress-1 deve rimanere attivo e funzionante
echo "Verifico Redis - Selective Cleanup"
echo "Path egress-2 vuoto:"
docker exec redis redis-cli GET tree:default-tree:session:broadcaster-1:path:egress-2
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Path egress-1 ancora presente:"
docker exec redis redis-cli GET tree:default-tree:session:broadcaster-1:path:egress-1
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Lista egress aggiornata - solo egress-1:"
docker exec redis redis-cli SMEMBERS tree:default-tree:session:broadcaster-1:egresses
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Relay-root ha ancora la session - serve egress-1:"
docker exec redis redis-cli SMEMBERS tree:default-tree:node:relay-root-1:sessions
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Egress-2 session rimossa:"
docker exec redis redis-cli SMEMBERS tree:default-tree:node:egress-2:sessions
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Routing aggiornata - solo egress-1:"
docker exec redis redis-cli SMEMBERS tree:default-tree:routing:broadcaster-1:relay-root-1
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Mountpoint egress-2 rimosso:"
docker exec redis redis-cli HGETALL tree:default-tree:mountpoint:egress-2:broadcaster-1
echo ""
echo "Selective cleanup corretto"
read -p "Premi invio per continuare..."

echo ""
echo "Verifico Egress-1 Ancora Funzionante"
echo "Browser 1 - egress-1 - localhost:7072 - video/audio dovrebbe funzionare (pallina)"
echo "Browser 2 - egress-2 - localhost:7073 - Il video dovrebbe essere interrotto"
read -p "Premi invio per continuare..."

echo ""
echo "=================================================="
echo "DISTRUZIONE COMPLETA SESSIONE"
echo "=================================================="
echo ""

# Ora distruggo l'intera sessione, devono essere rimossi tutti i path
# e tutti i dati devono essere puliti da Redis
echo "Distruggo l'Intera Sessione..."
curl -X DELETE http://localhost:8080/api/trees/default-tree/sessions/broadcaster-1
echo ""
echo ""
read -p "Premi invio per continuare..."

echo ""
# Verifico che TUTTO sia stato rimosso da Redis
# Non devono rimanere tracce della sessione broadcaster-1
echo "Verifico Complete Cleanup Redis"
echo "Session metadata vuota:"
docker exec redis redis-cli EXISTS tree:default-tree:session:broadcaster-1
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Lista egress vuota:"
docker exec redis redis-cli SMEMBERS tree:default-tree:session:broadcaster-1:egresses
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Path vuota:"
docker exec redis redis-cli GET tree:default-tree:session:broadcaster-1:path:egress-1
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Injection session vuota:"
docker exec redis redis-cli SMEMBERS tree:default-tree:node:injection-1:sessions
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Relay-root session vuota:"
docker exec redis redis-cli SMEMBERS tree:default-tree:node:relay-root-1:sessions
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Egress-1 session vuota:"
docker exec redis redis-cli SMEMBERS tree:default-tree:node:egress-1:sessions
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Routing table vuota:"
docker exec redis redis-cli SMEMBERS tree:default-tree:routing:broadcaster-1:relay-root-1
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Session tree index vuota:"
docker exec redis redis-cli SMEMBERS tree:default-tree:sessions
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Mountpoint egress-1 vuoto:"
docker exec redis redis-cli HGETALL tree:default-tree:mountpoint:egress-1:broadcaster-1
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Check residui - non devono esistere:"
docker exec redis redis-cli KEYS tree:default-tree:session:broadcaster-1:*
echo ""
echo "Cleanup completo"
read -p "Premi invio per continuare..."

echo ""
# Verifico i log dei nodi per confermare che abbiano ricevuto l'evento di distruzione
echo "Verifico i Nodi"
echo "Browser - egress-1 - mountpoint destroyed"
echo ""
read -p "Premi invio per verificare i logs..."

echo ""
echo "Injection node logs:"
docker logs injection-1 | tail -20
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Relay-root logs:"
docker logs relay-root-1 | tail -20
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Egress-1 logs:"
docker logs egress-1 | tail -20
echo ""
echo "Tutti i nodi notificati"
read -p "Premi invio per continuare..."

echo ""
echo "=================================================="
echo "CLEANUP"
echo "=================================================="
echo ""

# Distruggo anche l'albero e verifico che Redis sia completamente pulito
echo "Distruggo il tree..."
curl -X DELETE http://localhost:8080/api/trees/default-tree
echo ""
echo ""
read -p "Premi invio per continuare..."

echo ""
echo "Verifico Redis completamente pulito:"
docker exec redis redis-cli KEYS tree:default-tree:*
echo ""
echo "Reset completo:"
echo "docker-compose down -v"
echo "Test completato"
echo ""

