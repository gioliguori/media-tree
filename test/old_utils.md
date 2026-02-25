# Test

## Monitoraggio Real-Time
altro terminale:

```bash
watch -n 1 "echo '--- GLOBAL RELAY LOAD (0-20) ---'; \
docker exec redis redis-cli ZRANGE pool:relay:load 0 -1 WITHSCORES; \
echo ''; echo '--- ACTIVE CHAINS ---'; \
docker exec redis redis-cli KEYS 'session:*:chain' | xargs -I {} sh -c \"echo '{}:'; docker exec redis redis-cli LRANGE {} 0 -1 | tr '\n' ' ' ; echo ''\"; \
echo ''; echo '--- EDGE COUNTS ---'; \
docker exec redis redis-cli KEYS 'session:*:edge_counts' | xargs -I {} sh -c \"echo '{}:'; docker exec redis redis-cli HGETALL {}\""
```

---

## Prerequisiti e Setup
1. Reset Ambiente: `docker-compose down -v`
2. Avvio Servizi: `docker-compose up --build -d`

---

## ESPERIMENTO 1: Scalabilità Egress e Strategia Fill-First
**Obiettivo:** Verificare il buffer di sicurezza (Scaling UP), la strategia Fill-First e la sequenza di distruzione (Backtrack -> Draining -> Destroy).

**Passi dell'esperimento:**
1. **Apertura Sessione:** Creare un broadcaster.
   ```bash
   cd test && ./create-session.sh
   ```
2. **Trigger Scaling UP:** Aprire 6 tab del browser (viewer) collegate alla sessione. 
   *Poiché restano 4 posti liberi (10-6) e la soglia minima è 5, l'Autoscaler deve lanciare egress-2.*
3. **Saturazione Nodo 1:** Aprire altre 4 tab del browser (Totale 10 su egress-1).
   *Osservazione: Il selettore deve riempire egress-1 fino al limite fisico di 10/10.*
4. **Overflow su Nodo 2:** Aprire la 11° tab. 
   *Osservazione: Il selettore deve instradare il viewer su egress-2.*
5. **Draining e Backtracking:** Chiudere le 10 tab collegate a egress-1.
   *Osservazione: L'Autoscaler mette egress-1 in stato DRAINING. Il SessionCleanup rileva 0 spettatori fisici, lancia il backtracking rimuovendo i percorsi logici. Solo dopo che il mountpoint è rimosso dal SET Redis, il container egress-1 viene distrutto.*
6. **Cleanup Finale:** Chiudere il viewer su egress-2 e distruggere il broadcaster (kill whip client).
   *Osservazione: Rimozione totale di tutti i mountpoint e dei percorsi logici rimanenti.*

---

## ESPERIMENTO 2: Scalabilità Injection e Load Balancing
**Obiettivo:** Validare lo scaling della coppia d'ingresso (Injection + Relay-Root) e la distribuzione del carico.

**Passi dell'esperimento:**
1. **Sessioni Reali:** Avviare una sessione tramite script `./create-session.sh`.
2. **Saturazione Artificiale:** Simulare il carico quasi totale sul primo nodo:
   ```bash
   docker exec redis redis-cli ZADD pool:injection:load 9 injection-1
   ```
3. **Verifica Scale UP:** Attendere il tick dell'Autoscaler.
   *Osservazione: Viene creata la coppia injection-2 + relay-root-2.*
4. **Verifica Load Balancing:** Creare una nuova sessione reale.
   *Osservazione: Il selettore deve assegnarla a injection-2 perché injection-1 è saturo.*
5. **Ripristino e Scale Down:** Riportare injection-1 al carico reale (es. 1 sessione):
   ```bash
   docker exec redis redis-cli ZADD pool:injection:load 1 injection-1
   ```
6. **Draining:** Attendere il tick dell'Autoscaler.
   *Osservazione: injection-1 viene messo in DRAINING poiché è il nodo meno carico e la capacità totale è eccedente.*
7. **Verifica Cascading Destroy:** Rimuovere la sessione residua su injection-1 (distruggere il relativo whip client).
   *Osservazione: Il job di cleanup distrugge sessione e path. L'Autoscaler rileva il carico logico a 0 e distrugge injection-1 eliminando a cascata anche il figlio statico relay-root-1.*

---

## ESPERIMENTO 3: Dinamismo Mesh (Hole-Filling vs Deepening)
**Obiettivo:** Validare la logica degli slot Relay (1 slot per Hole-Filling, 2 slot per Deepening) e il pruning dei rami morti.

**Passi dell'esperimento:**
1. **Creazione Sessione:** Avviare una sessione completa con percorso mesh.
2. **Saturazione Parziale Relay:** Portare relay-1 a 19 slot occupati su 20 (ne resta 1 libero):
   ```bash
   docker exec redis redis-cli ZADD pool:relay:load 19 relay-1
   ```
3. **Hole-Filling (Riuso):** Saturare egress-1 con 10 viewer e richiedere un nuovo viewer per la stessa sessione (che verrà assegnato a egress-2).
   *Osservazione: Il carico di relay-1 passa a 20. Il sistema usa l'ultimo slot disponibile per collegare il nuovo egress al flusso già esistente senza creare nuovi salti.*
4. **Saturazione Totale:** Portare relay-1 a 20/20 forzatamente:
   ```bash
   docker exec redis redis-cli ZADD pool:relay:load 20 relay-1
   ```
5. **Deepening (Allungamento):** Richiedere un nuovo viewer per una sessione che non è ancora presente su relay-1.
   *Osservazione: relay-1 è pieno. Il sistema deve creare relay-2 e allungare la catena: [root -> relay-1 -> relay-2 -> egress].*
6. **Verifica Deepening su Nuova Sessione:** Creare una nuova Session-B e richiedere un viewer.
   *Osservazione: Session-B richiede 2 slot (Reserve + Edge). Non potendo stare su relay-1 (pieno), viene assegnata direttamente a relay-2.*
7. **Backtracking & Pruning:** Chiudere i viewer che rendono relay-2 l'unico anello finale della catena.
   *Osservazione: Il log deve mostrare "Pruning dead branch: relay-2". La sessione viene rimossa dal nodo, lo slot su relay-1 viene liberato e relay-2 viene spento dall'Autoscaler.*

---

## Comandi Utili per la Diagnostica

### Controllo Carichi (ZSET)
```bash
# Injection Pool
docker exec redis redis-cli ZRANGE pool:injection:load 0 -1 WITHSCORES
# Relay Pool
docker exec redis redis-cli ZRANGE pool:relay:load 0 -1 WITHSCORES
```

### Controllo Sessioni Logiche (SET)
```bash
# Mountpoints su un nodo Egress
docker exec redis redis-cli SMEMBERS node:egress-1:mountpoints
# Sessioni su un nodo Relay
docker exec redis redis-cli SMEMBERS node:relay-1:sessions
```

### Reset Manuale (In caso di emergenza)
```bash
# Rimuovere tutti i lock di scaling
docker exec redis redis-cli KEYS 'lock:scaling:*' | xargs docker exec redis redis-cli DEL
```

---

## Conclusioni del Test
Il superamento di questi tre esperimenti garantisce che:
1. Il **Data Plane (RTP)** fluisce correttamente attraverso nodi dinamici.
2. Il **Control Plane (Go)** gestisce le race conditions tramite coordinamento su Redis.
3. L'**Algoritmo di Routing** minimizza il numero di relay attivi tramite strategie di consolidamento (Fill-First e Hole-Filling).