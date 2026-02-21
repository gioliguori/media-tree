#!/bin/bash

set -e

REGISTRY="localhost:5888"
echo "Building Media Tree Docker images for Registry: $REGISTRY"

#  Cleanup
echo "Pulizia ambiente pre-build..."
kubectl delete pods -l app=media-tree --ignore-not-found || true
# Svuota Redis
kubectl exec -it deployment/redis -- redis-cli FLUSHALL 2>/dev/null || true

# Vai alla root del progetto
cd "$(dirname "$0")"

echo "Building Janus components..."

# Janus VideoRoom
docker build -t $REGISTRY/media-tree/janus-videoroom:latest \
  -f janus-videoroom/Dockerfile janus-videoroom/
docker push $REGISTRY/media-tree/janus-videoroom:latest

# Janus Streaming
docker build -t $REGISTRY/media-tree/janus-streaming:latest \
  -f janus-streaming/Dockerfile janus-streaming/
docker push $REGISTRY/media-tree/janus-streaming:latest

echo "Building Node.js services and Controller..."

# Controller
docker build -t $REGISTRY/media-tree/controller:latest \
  -f controller/Dockerfile controller/
docker push $REGISTRY/media-tree/controller:latest

# Injection Node
docker build -t $REGISTRY/media-tree/injection-node:latest \
  -f injection-node/Dockerfile .
docker push $REGISTRY/media-tree/injection-node:latest

# Relay Node
docker build -t $REGISTRY/media-tree/relay-node:latest \
  -f relay-node/Dockerfile .
docker push $REGISTRY/media-tree/relay-node:latest

# Egress Node
docker build -t $REGISTRY/media-tree/egress-node:latest \
  -f egress-node/Dockerfile .
docker push $REGISTRY/media-tree/egress-node:latest

# Metrics Agent
# docker build -t $REGISTRY/media-tree/metrics-agent:latest \
#   ./metrics-agent/
# docker push $REGISTRY/media-tree/metrics-agent:latest

echo "All images built and pushed to $REGISTRY successfully!"