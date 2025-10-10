
cd relay-node

docker compose -f docker-compose.test.yaml build

## SCENARIO 1: sender → relay-1 → receiver-1

docker compose -f docker-compose.test.yaml up -d redis relay-1 sender receiver-1

# Registra receiver-1
docker exec -it relay-node-redis-1 redis-cli HSET node:receiver-1 \
  id receiver-1 \
  type egress \
  host rtp-receiver-1 \
  port 7072 \
  audioPort 6000 \
  videoPort 6002 \
  status active


docker exec -it relay-node-redis-1 redis-cli EXPIRE node:receiver-1 300

# Imposta relazione parent/child
docker exec -it relay-node-redis-1 redis-cli SADD children:relay-1 receiver-1
docker exec -it relay-node-redis-1 redis-cli SET parent:receiver-1 relay-1

# Verifica
docker exec -it relay-node-redis-1 redis-cli SMEMBERS children:relay-1

docker exec -it relay-node-redis-1 redis-cli GET parent:receiver-1

sleep 35

# Controlla log
docker logs relay-1 | tail -20

# COntrollare receiver 2 modi
docker logs rtp-receiver-1 | tail -30

# Installa tcpdump
docker exec -it rtp-receiver-1 sh -c 'apt-get update && apt-get install -y tcpdump'

# Cattura pacchetti UDP sulla porta audio
docker exec -it rtp-receiver-1 tcpdump -i eth0 -n udp port 6000 -c 10

# Verifica stats
curl http://localhost:7070/status | jq '.stats'
# {
#   "audio": {
#     "packetsReceived": 450,
#     "packetsForwarded": 450,
#     ...
#   },
#   "video": { ... }
# }

# Cleanup
docker compose -f docker-compose.test.yaml down

## SCENARIO 2: Dynamic Children

# Partire da scenario 1 

# Avvia receiver-2
docker compose -f docker-compose.test.yaml up -d receiver-2

# Registra receiver-2
docker exec -it relay-node-redis-1 redis-cli HSET node:receiver-2 \
  id receiver-2 \
  type egress \
  host rtp-receiver-2 \
  port 7073 \
  audioPort 6100 \
  videoPort 6102 \
  status active

docker exec -it relay-node-redis-1 redis-cli EXPIRE node:receiver-2 300
docker exec -it relay-node-redis-1 redis-cli SADD children:relay-1 receiver-2
docker exec -it relay-node-redis-1 redis-cli SET parent:receiver-2 relay-1

# Aspetta e verifica
sleep 35

# Controlla log
docker logs relay-1 | tail -20


# Verifica entrambi i receiver
docker logs rtp-receiver-1 | tail -10
docker logs rtp-receiver-2 | tail -10

# Installa tcpdump
docker exec -it rtp-receiver-1 sh -c 'apt-get update && apt-get install -y tcpdump'

# Cattura pacchetti UDP sulla porta audio
docker exec -it rtp-receiver-1 tcpdump -i eth0 -n udp port 6000 -c 10

# Installa tcpdump
docker exec -it rtp-receiver-2 sh -c 'apt-get update && apt-get install -y tcpdump'

# Cattura pacchetti UDP sulla porta audio
docker exec -it rtp-receiver-2 tcpdump -i eth0 -n udp port 6100 -c 10

# SCENARIO 3: Relay Chain

# Avvia tutto
docker compose -f docker-compose.test.yaml --profile scenario3 up -d

# Configura catena

# Registra receiver-1
docker exec -it relay-node-redis-1 redis-cli HSET node:receiver-1 \
  id receiver-1 \
  type egress \
  host rtp-receiver-1 \
  port 7072 \
  audioPort 6000 \
  videoPort 6002 \
  status active

docker exec -it relay-node-redis-1 redis-cli EXPIRE node:receiver-1 300

# relay-1 → relay-2
docker exec -it relay-node-redis-1 redis-cli SADD children:relay-1 relay-2
docker exec -it relay-node-redis-1 redis-cli SET parent:relay-2 relay-1


# relay-2 → receiver-1
docker exec -it relay-node-redis-1 redis-cli SADD children:relay-2 receiver-1
docker exec -it relay-node-redis-1 redis-cli SET parent:receiver-1 relay-2

# Verifica

# Catena configurata
docker exec -it relay-node-redis-1 redis-cli SMEMBERS children:relay-1
# Output: "relay-2"

docker exec -it relay-node-redis-1 redis-cli SMEMBERS children:relay-2
# Output: "receiver-1"

sleep 35
docker logs relay-1 | grep "Adding children"
docker logs relay-2 | grep "Adding children"

# Stats entrambi i relay
curl http://localhost:7070/status | jq '.stats'
curl http://localhost:7071/status | jq '.stats'

# Verifica ricezione finale
docker logs rtp-receiver-1 | tail -30

# tcpdump su relay-2
docker exec -it relay-2 sh -c 'apt-get update && apt-get install -y tcpdump'
docker exec -it relay-2 tcpdump -i eth0 -n udp port 5102 -c 10

# tcpdump sul receiver
docker exec -it rtp-receiver-1 sh -c 'apt-get update && apt-get install -y tcpdump'
docker exec -it rtp-receiver-1 tcpdump -i eth0 -n udp port 6000 -c 10


docker compose -f docker-compose.test.yaml down
docker compose -f docker-compose.test.yaml --profile scenario3 down