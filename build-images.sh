#!/bin/bash

set -e

echo "Building Media Tree Docker images..."

# Vai alla root del progetto
cd "$(dirname "$0")"

# Build Janus VideoRoom
echo "Building janus-videoroom..."
docker build -t media-tree/janus-videoroom:latest \
  -f janus-videoroom/Dockerfile \
  janus-videoroom/

# Build Janus Streaming
echo "Building janus-streaming..."
docker build -t media-tree/janus-streaming:latest \
  -f janus-streaming/Dockerfile \
  janus-streaming/

# Build Injection Node
echo "Building injection-node..."
docker build -t media-tree/injection-node:latest \
  -f injection-node/Dockerfile \
  .

# Build Relay Node
echo "Building relay-node..."
docker build -t media-tree/relay-node:latest \
  -f relay-node/Dockerfile \
  .

# Build Egress Node
echo "Building egress-node..."
docker build -t media-tree/egress-node:latest \
  -f egress-node/Dockerfile \
  .

echo "✅ All images built successfully!"
echo ""
echo "Available images:"
docker images | grep media-tree