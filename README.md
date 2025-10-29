# media-tree
PROBLEMI 


IMPROVEMENTS
- relay node (egress anche) funziona ma distrugge e ricrea pipeline, (usare multiudpsink (non esistono api in javascript, dobbiamo capire come fare)) 
- azioni database forse hanno bisogno di transazioni, inoltre ora polling (forse meglio pub/sub, però devo ancora esplorare soluzione)
- nodi non fanno recovery status se crashano (commento egress-node/src/EgressNode.js) (tenere conto anche di  port pool recovery)
- cross node coordination?