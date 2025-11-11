# media-tree
PROBLEMI 
- Injection node
    - nessun problema rilevante al momento
- relay node (C) (stesse considerazioni per egress-node (C))
    - Nodejs crasha = tutto giù
    - Nessun auto-restart
    - Ogni tot lista destinazioni e confrontiamo con redis
    - Mancano stats varie

- Egress node 
    - list con cJSON
    - creazione pad ssrc funziona solo se mountpoint -> forwarding, se inviamo pacchetti prima di creare il mountpoint non funziona
    - distruzione invece al contrario, se distruggiamo mountpoint mentre facciamo forwarding va in errore, bisogna prima stoppare il forwarder (o magari distruggere sessione videoroom) 


- Generali
    - Non gestiamo le azioni di creazione/distruzione tramite transazioni
    - facciamo polling su database ma conviene forse fare pub/sub
    - nodi non fanno recovery dello stato
    - approfondire GMainLoop
