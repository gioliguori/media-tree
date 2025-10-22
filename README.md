# media-tree

- injection node non funziona quando cambiamo children (serve updateForwarders())
- relay node (egress anche) funziona ma distrugge e ricrea pipeline, (usare multiudpsink (non esistono api in javascript, dobbiamo capire come fare)) 
- non funziona distruggere sessioni (sia lato videoroom che lato streaming)