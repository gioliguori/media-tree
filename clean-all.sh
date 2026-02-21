#!/bin/bash

echo "--- CLEANUP TOTALE MEDIA-TREE ---"

# Ferma il controller
kubectl scale deployment controller --replicas=0 2>/dev/null || true

echo "Rimozoine Pod della mesh..."
kubectl delete pods -l app=media-tree --ignore-not-found

# Svuota Redis
echo "Svuotamento Redis..."
kubectl exec -it deployment/redis -- redis-cli FLUSHALL || true

# echo "Eliminazione cluster k3d..."
k3d cluster delete media-tree
k3d registry delete media-registry

echo "--- CLEANUP COMPLETATO ---"