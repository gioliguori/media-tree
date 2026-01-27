#!/bin/bash

# build e setup
echo "Build immagini"
cd ..
./build-images.sh

echo "Avvio Controller e Redis"
cd controller
docker compose down
docker rm -f $(docker ps -aq --filter "name=whip-") 2>/dev/null
docker compose up --build -d controller redis

sleep 15

cd ../test
chmod +x ./create-session.sh

# saturazione nodo 1
echo "Saturazione nodo 1 (10 sessioni)"
for i in {1..10}; do
   ./create-session.sh "n1-$i" > /dev/null &
   sleep 1
done
wait
echo "Nodo 1 pieno. Attendo Scale Up (70s)"
sleep 70
read -p "Premi invio per continuare"

# saturazione nodo 2
echo "Saturazione Nodo 2 (10 sessioni)"
for i in {1..10}; do
   ./create-session.sh "n2-$i" > /dev/null &
   sleep 1
done
wait
echo "Nodo 2 pieno. Attendo Scale Up (70s)"
sleep 70

read -p "Premi invio per continuare"

# saturazione nodo 3 (non completamente)
echo "Riempimento parziale Nodo 3 (5 sessioni)"
for i in {1..5}; do
   ./create-session.sh "n3-$i" > /dev/null &
   sleep 1
done
wait
echo "Nodo 3 a metà carico."
sleep 10

read -p "Premi invio per continuare"

# Scale down
echo "Kill dei client (nodo 1 e 2)"
docker rm -f $(docker ps -q -f "name=whip-n1-")
docker rm -f $(docker ps -q -f "name=whip-n2-")

echo "Client uccisi. Attendo scaling (120s)"
sleep 120

read -p "Premi invio per continuare"

# Scale up di nuovo
echo "Saturazione nodo 3 (altre 5 sessioni)"
for i in {6..10}; do
   ./create-session.sh "n3-$i" > /dev/null &
   sleep 1
done
wait
sleep 70
read -p "Premi invio per continuare"

echo "Test completato"

# Cleanup
echo "Pulizia finale"
sleep 10
cd ../controller
docker compose down
docker rm -f $(docker ps -aq --filter "name=whip-") 2>/dev/null