# Test

## Monitoraggio Real-Time
in terminale a parte.
```bash
watch -n 1 "echo '--- PODS K8S ATTIVI ---'; \
kubectl get pods -l app=media-tree; \
echo ''; echo '--- GLOBAL RELAY LOAD (0-20) ---'; \
kubectl exec deployment/redis -- redis-cli --raw ZRANGE pool:relay:load 0 -1 WITHSCORES; \
echo ''; echo '--- ACTIVE CHAINS (Path Logici) ---'; \
kubectl exec deployment/redis -- redis-cli --raw KEYS 'session:*:chain' | xargs -I {} sh -c \"echo '{}:'; kubectl exec deployment/redis -- redis-cli --raw LRANGE {} 0 -1 | tr '\n' ' ' ; echo ''\"; \
echo ''; echo '--- EDGE COUNTS (Slot Occupati) ---'; \
kubectl exec deployment/redis -- redis-cli --raw KEYS 'session:*:edge_counts' | xargs -I {} sh -c \"echo '{}:'; kubectl exec deployment/redis -- redis-cli --raw HGETALL {}\""
```

---

## Prerequisiti e Setup
* Reset Totale: ./clean-all.sh (elimina cluster k3d e registry).
* Bootstrap: ./setup-lab.sh (crea cluster, builda immagini e fa il deploy iniziale).
* Port Forwarding:
```bash
# Per le API del Controller
kubectl port-forward svc/media-controller 8080:8080
# Per ispezionare Redis
kubectl port-forward svc/redis 6379:6379
```

---

## ESPERIMENTO 1: Scalabilità Egress e Strategia Fill-First
Obiettivo: Verificare Scaling UP, la strategia Fill-First e la sequenza di distruzione.

Passi:
* Apertura Sessione: Crea un broadcaster via API.
```bash
curl -X POST http://localhost:8080/api/sessions \
-H "Content-Type: application/json" \
-d '{"sessionId":"broadcaster-1"}' | jq
```
flusso con OBS 

* Trigger Scaling UP: Aprire 6 tab del browser collegate alla sessione.
(Logica: TotalFreeSlots scende a 4. Poiché la soglia minima MinFreeSlotsEgress è 5, l'Autoscaler deve lanciare egress-2).

* Saturazione Nodo 1: Aprire altre 4 tab del browser (Totale 10 spettatori).
Osservazione: Il selettore deve continuare a riempire egress-1 fino al limite fisico di 10/10 spettatori (Strategia Fill-First).

* Overflow su Nodo 2: Aprire l'11° tab del browser. 
Osservazione: Il selettore, vedendo egress-1 pieno, deve ora indirizzare il viewer su egress-2.

* Draining e Backtracking: Chiudere le 10 tab collegate a egress-1.
Osservazione:
* L'Autoscaler metterà egress-1 in stato DRAINING.
* Il SessionCleanup rileverà 0 spettatori fisici su quel percorso.
* Verrà lanciato il backtracking che rimuoverà i percorsi logici dalla mesh.
* Quando il nodo è logicamente vuoto, il Controller eseguirà kubectl delete pod egress-1.

* Cleanup Finale: Chiudere l'ultimo viewer su egress-2 e fermare il broadcaster.
Osservazione: Rimozione totale di tutti i dati residui.

---

## ESPERIMENTO 2: Scalabilità Injection e Load Balancing
Obiettivo: Validare lo scaling della coppia d'ingresso (Injection + Relay-Root).

Passi:
* Creazione Sessione: Avvia una sessione.
```bash
curl -X POST http://localhost:8080/api/sessions \
-H "Content-Type: application/json" \
-d '{"sessionId":"broadcaster-1"}' | jq
```

* Saturazione Artificiale: Simula il carico quasi totale sul primo nodo (9 sessioni su 10):
```bash
kubectl exec -it deployment/redis -- redis-cli ZADD pool:injection:load 9 injection-1
```

* Verifica Scale UP: Attendere il prossimo tick dell'Autoscaler (max 20s).
Osservazione: Viene creato il Pod injection-2 che contiene al suo interno anche relay-root-2.

* Verifica Load Balancing: Crea una nuova sessione reale (broadcaster-2).
Osservazione: Il selettore assegnerà la sessione a injection-2 perché injection-1 è considerato quasi saturo.

* Ripristino e Scale Down: Riportare injection-1 al carico reale (es. 1 sessione):
```bash
kubectl exec -it deployment/redis -- redis-cli ZADD pool:injection:load 1 injection-1
```

* Draining: Attendere il tick dell'Autoscaler.
Osservazione: injection-1 viene messo in DRAINING perché è il meno carico e c'è eccesso di capacità.

* Verifica Cascading Destroy: Fermare il broadcaster collegato a injection-1.
Osservazione: L'Autoscaler distruggerà il Pod injection-1. Kubernetes eliminerà tutti i container nel pod, inclusa la relay-root-1. Il Controller deve pulire tutte le chiavi di entrambi i nodi da Redis.

---

## ESPERIMENTO 3: Dinamismo Mesh Profondo (Hole-Filling, Deepening e Pruning)

Obiettivo: Validare la capacità del Controller di allungare la catena di trasmissione quando i nodi intermedi sono saturi e di potare i rami morti

Fase 1: Setup e Hole-Filling
* Avvio: Creare la broadcaster-1 e aprire 1 viewer.
Stato atteso: La catena è [relay-root-1, relay-1]. Il viewer è su egress-1.
* Saturazione Artificiale (Quasi piena): Portare relay-1 a 19 slot occupati su 20:
```bash
kubectl exec -it deployment/redis -- redis-cli ZADD pool:relay:load 19 relay-1
```
* Hole-Filling (Riuso): Aprire tab finché egress-1 è pieno (10 viewer). Il sistema allocherà il prossimo viewer su egress-2.
Osservazione: relay-1 passerà a 20/20. Il sistema ha usato l'ultimo slot per riutilizzare il flusso esistente. La catena rimane [relay-root-1, relay-1].
* Svuotamento Parziale: Chiudere i viewer su egress-2 e attendere distruzione path:
Stato atteso: Il carico di relay-1 torna a 19.

Fase 2: Deepening (Saturazione e Allungamento)
* Saturazione Totale Manuale: Forza relay-1 a 20/20:
```bash
kubectl exec -it deployment/redis -- redis-cli ZADD pool:relay:load 20 relay-1
```
* Allungamento Catena: Richiedere un nuovo viewer (finirà su egress-2).
Logica: relay-1 è pieno (20/20). Il selettore non può aggiungere altri viewer lì. Deve trovare un nuovo relay (relay-2) e appenderlo alla catena.
Osservazione: Nel monitoraggio la catena si allungherà: [relay-root-1, relay-1, relay-2].
Verifica: Il nuovo viewer su egress-2 ora riceve il video passando per due salti di relay standalone.

Fase 3: Cleanup
* Rimozione dell'ultimo anello: Chiudere il viewer su egress-2.
Osservazione: Il sistema nota che relay-2 non serve più a nessuno (Edge Count = 0).
Backtracking:
* Viene rimosso il path verso egress-2.
* relay-2 viene rimosso dalla catena logica.
* Lo slot occupato su relay-1 (che serviva per alimentare relay-2) non viene liberato perché è lo slot deepening.