# media-tree
PROBLEMI 
- Egress node (C)  
    - creazione pad ssrc funziona solo se mountpoint -> forwarding, se inviamo pacchetti prima di creare il mountpoint non funziona
    - distruzione invece al contrario, se distruggiamo mountpoint mentre facciamo forwarding va in errore, bisogna prima stoppare il forwarder (o magari distruggere sessione videoroom) 

- Generali
    - Non gestiamo le azioni di creazione/distruzione tramite transazioni
    - Mancano stats varie