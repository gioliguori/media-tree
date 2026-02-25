#!/bin/bash

# Cleanup iniziale
echo "Pulisco vecchi cluster..."
k3d cluster delete media-tree || true
k3d registry delete media-registry || true

# Pulisce i volumi k3d orfani
# k3d cluster delete non rimuove i volumi Docker associati a k3s,
# che si corrompono tra una sessione e l'altra.
echo "Pulisco volumi k3d orfani..."
docker volume ls --filter "label=app=k3d" -q | xargs -r docker volume rm 2>/dev/null || true
docker volume ls --filter "name=k3d-media-tree" -q | xargs -r docker volume rm 2>/dev/null || true

# Crea il registry locale
k3d registry create media-registry --port 5888 || true

# 2. Crea il cluster k3d con 3 worker
# WebRTC UDP (20 porte per agent):
#   Agent 0: 20000-20020
#   Agent 1: 20100-20120
#   Agent 2: 20200-20220

k3d cluster create media-tree \
  --registry-use k3d-media-registry:5888 \
  --agents 3 \
  --api-port 6443 \
  --port "11000:11000@agent:0" --port "20000-20010:20000-20010/udp@agent:0" \
  --port "12000:12000@agent:1" --port "20100-20110:20100-20110/udp@agent:1" \
  --port "13000:13000@agent:2" --port "20200-20220:20200-20220/udp@agent:2" \
  --k3s-arg "--disable=traefik@server:0"

echo "Waiting for API Server to respond..."
until kubectl get nodes &> /dev/null; do sleep 2; done

# Installazione Metrics Server
echo "Installing Metrics Server..."
kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml --validate=false

# Patch per saltare TLS in locale
kubectl patch deployment metrics-server -n kube-system \
  --type 'json' \
  -p '[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'

# Attendi che il cluster sia pronto
echo "Waiting for nodes to be ready..."
kubectl wait --for=condition=Ready nodes --all --timeout=120s

echo "Next: ./build-images.sh && kubectl apply -f k8s/"

# Build e Push delle immagini
chmod +x ./build-images.sh
./build-images.sh

kubectl apply -f k8s/
echo ""
echo "in altro terminale"
echo ""
echo "kubectl port-forward svc/media-controller 8080:8080"
echo ""
echo "kubectl port-forward svc/redis 6379:6379"
