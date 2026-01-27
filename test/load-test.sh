#!/bin/bash
# Lancia 10 sessioni in parallelo per generare carico
for i in {1..10}; do
   echo "Lancio sessione load2-$i..."
   ./create-session.sh "load2-$i" &
   sleep 2 # Aspetta un attimo tra una e l'altra per non intasare l'API subito
done

wait
echo "Tutte le sessioni lanciate."