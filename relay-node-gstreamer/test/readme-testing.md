cd relay-node-gstreamer

docker compose -f docker-compose.test.yaml build

## SCENARIO 1: sender → relay-gst-1 → receiver-1

docker compose -f docker-compose.test.yaml up -d redis relay-gst-1 sender receiver-1

# Registra receiver-1
docker exec -it relay-node-gstreamer-redis-1 redis-cli HSET node:receiver-1 \
  id receiver-1 \
  type egress \
  host rtp-receiver-gst-1 \
  port 7072 \
  audioPort 6000 \
  videoPort 6002 \
  status active

docker exec -it relay-node-gstreamer-redis-1 redis-cli EXPIRE node:receiver-1 300

# Imposta relazione parent/child
docker exec -it relay-node-gstreamer-redis-1 redis-cli SADD children:relay-gst-1 receiver-1
docker exec -it relay-node-gstreamer-redis-1 redis-cli SET parent:receiver-1 relay-gst-1


# Verifica
docker exec -it relay-node-gstreamer-redis-1 redis-cli SMEMBERS children:relay-gst-1
# Output: "receiver-1"

docker exec -it relay-node-gstreamer-redis-1 redis-cli GET parent:receiver-1
# Output: "relay-gst-1"

sleep 35

# Controlla log
docker logs relay-gst-1 | tail -30

[relay-gst-1] Children changed (+1 -0)
[relay-gst-1] Added children: [ 'receiver-1' ]
[relay-gst-1] Rebuilding GStreamer pipelines...
[relay-gst-1] Starting pipelines for 1 children
[relay-gst-1] Audio pipeline: gst-launch-1.0 udpsrc port=5002 caps=application/x-rtp,media=audio,encoding-name=OPUS,clock-rate=48000,payload=111 ! udpsink host=rtp-receiver-gst-1 port=6000 sync=false async=false
[relay-gst-1] Video pipeline: gst-launch-1.0 udpsrc port=5004 caps=application/x-rtp,media=video,encoding-name=VP8,clock-rate=90000,payload=96 ! udpsink host=rtp-receiver-gst-1 port=6002 sync=false async=false

# COntrollare receiver 2 modi

docker logs rtp-receiver-gst-1 | tail -30

# Setting pipeline to PLAYING ...


# Installa tcpdump
docker exec -it rtp-receiver-gst-1 sh -c 'apt-get update && apt-get install -y tcpdump'

# Cattura pacchetti UDP sulla porta audio
docker exec -it rtp-receiver-gst-1 tcpdump -i eth0 -n udp port 6000 -c 10


# Verifica stats
curl http://localhost:7070/status | jq
curl http://localhost:7070/status | jq '.stats'

# Output:
# json{
#   "healthy": true,
#   "nodeId": "relay-gst-1",
#   "nodeType": "relay",
#   "gstreamer": {
#     "audioRunning": true,
#     "videoRunning": true,
#     "stats": {
#       "audioRestarts": 1,
#       "videoRestarts": 1,
#       "lastRestart": "2025-01-..."
#     }
#   },
#   "forwarding": {
#     "childrenCount": 1,
#     "children": ["receiver-1"]
#   }
# }

# Cleanup
docker compose -f docker-compose.test.yaml down

## SCENARIO 2: Dynamic Children

# Partire da scenario 1 

# Avvia receiver-2
docker compose -f docker-compose.test.yaml --profile scenario2 up -d receiver-2

# Registra receiver-2
docker exec -it relay-node-gstreamer-redis-1 redis-cli HSET node:receiver-2 \
  id receiver-2 \
  type egress \
  host rtp-receiver-gst-2 \
  port 7073 \
  audioPort 6100 \
  videoPort 6102 \
  status active

docker exec -it relay-node-gstreamer-redis-1 redis-cli EXPIRE node:receiver-2 300
docker exec -it relay-node-gstreamer-redis-1 redis-cli SADD children:relay-gst-1 receiver-2
docker exec -it relay-node-gstreamer-redis-1 redis-cli SET parent:receiver-2 relay-gst-1


# Aspetta e verifica
sleep 35

# Controlla log
docker logs relay-gst-1 | tail -30
# (tee)

# [relay-gst-1] Children changed (+1 -0)
# [relay-gst-1] Added children: [ 'receiver-2' ]
# [relay-gst-1] Rebuilding GStreamer pipelines...
# [relay-gst-1] Stopping audio pipeline...
# [relay-gst-1] Stopping video pipeline...
# [relay-gst-1] Starting pipelines for 2 children
# [relay-gst-1] Audio pipeline: gst-launch-1.0 udpsrc port=5002 caps=... ! tee name=t_audio allow-not-linked=true t_audio. ! queue max-size-buffers=200 leaky=downstream ! udpsink host=rtp-receiver-gst-1 port=6000 sync=false async=false t_audio. ! queue max-size-buffers=200 leaky=downstream ! udpsink host=rtp-receiver-gst-2 port=6100 sync=false async=false

# Verifica entrambi i receiver
docker logs rtp-receiver-gst-1 | tail -10
docker logs rtp-receiver-gst-2 | tail -10

# Installa tcpdump
docker exec -it rtp-receiver-gst-1 sh -c 'apt-get update && apt-get install -y tcpdump'

# Cattura pacchetti UDP sulla porta audio
docker exec -it rtp-receiver-gst-1 tcpdump -i eth0 -n udp port 6000 -c 10

# Installa tcpdump
docker exec -it rtp-receiver-gst-2 sh -c 'apt-get update && apt-get install -y tcpdump'

# Cattura pacchetti UDP sulla porta audio
docker exec -it rtp-receiver-gst-2 tcpdump -i eth0 -n udp port 6100 -c 10



# SCENARIO 3: Relay Chain

# Avvia tutto
docker compose -f docker-compose.test.yaml --profile scenario3 up -d

# Configura catena relay-gst-1 → relay-gst-2 → receiver-1

# Registra receiver-1
docker exec -it relay-node-gstreamer-redis-1 redis-cli HSET node:receiver-1 \
  id receiver-1 \
  type egress \
  host rtp-receiver-gst-1 \
  port 7072 \
  audioPort 6000 \
  videoPort 6002 \
  status active

docker exec -it relay-node-gstreamer-redis-1 redis-cli EXPIRE node:receiver-1 300


# relay-gst-1 → relay-gst-2
docker exec -it relay-node-gstreamer-redis-1 redis-cli SADD children:relay-gst-1 relay-gst-2
docker exec -it relay-node-gstreamer-redis-1 redis-cli SET parent:relay-gst-2 relay-gst-1

# relay-gst-2 → receiver-1
docker exec -it relay-node-gstreamer-redis-1 redis-cli SADD children:relay-gst-2 receiver-1
docker exec -it relay-node-gstreamer-redis-1 redis-cli SET parent:receiver-1 relay-gst-2


# Verifica

# Catena configurata
docker exec -it relay-node-gstreamer-redis-1 redis-cli SMEMBERS children:relay-gst-1
# Output: "relay-gst-2"

docker exec -it relay-node-gstreamer-redis-1 redis-cli SMEMBERS children:relay-gst-2
# Output: "receiver-1"

sleep 35

# Controlla pipeline relay-gst-1
docker logs relay-gst-1 | grep "pipeline"

# Controlla pipeline relay-gst-2
docker logs relay-gst-2 | grep "pipeline"

# Stats entrambi i relay
curl http://localhost:7070/status | jq '.stats'
curl http://localhost:7071/status | jq '.stats'
curl http://localhost:7070/status | jq '.forwarding'
curl http://localhost:7071/status | jq '.forwarding'

# Verifica ricezione finale
docker logs rtp-receiver-gst-1 | tail -20


# tcpdump su relay-2
docker exec -it relay-gst-2 sh -c 'apt-get update && apt-get install -y tcpdump'
docker exec -it relay-gst-2 tcpdump -i eth0 -n udp port 5102 -c 10

# tcpdump sul receiver
docker exec -it rtp-receiver-gst-1 sh -c 'apt-get update && apt-get install -y tcpdump'
docker exec -it rtp-receiver-gst-1 tcpdump -i eth0 -n udp port 6000 -c 10

docker compose -f docker-compose.test.yaml down
docker compose -f docker-compose.test.yaml --profile scenario3 down

Troubleshooting
# Verifica GStreamer
docker exec -it relay-gst-1 gst-launch-1.0 --version

# Verifica log dettagliati
docker logs relay-gst-1 -f
docker logs rtp-sender-gst | grep "Setting pipeline"

# Verifica porte aperte
docker exec -it relay-gst-1 netstat -uln | grep 5002

# Verifica connettività
docker exec -it relay-gst-1 ping rtp-receiver-gst-1
