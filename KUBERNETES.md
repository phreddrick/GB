# Kubernetes Deployment Guide for Galactic Bloodshed

## Overview

This guide explains how to build and deploy Galactic Bloodshed to a Kubernetes cluster.

## Prerequisites

- Docker installed locally
- `kubectl` configured and pointing to a Kubernetes cluster
- At least 1Gi storage available (for persistent volume)

## Build Steps

### 1. Build the Docker Image

From the repository root:

```bash
docker build -t gb:latest .
```

This creates a multi-stage build that:
- Compiles all Go binaries (makeuniv, gb-server, racegen)
- Packages them into a minimal Alpine Linux image
- Total image size: ~50-100MB

### 2. Load Image into Kubernetes

**For local/minikube testing:**
```bash
# If using minikube:
minikube image load gb:latest

# Or Docker Desktop Kubernetes:
docker image ls | grep gb  # Should show gb:latest
```

**For production registry:**
```bash
docker tag gb:latest your-registry.com/gb:latest
docker push your-registry.com/gb:latest

# Then edit k8s-deployment.yaml:
# Change: imagePullPolicy: Never
# To:     imagePullPolicy: Always
# And:    image: gb:latest
# To:     image: your-registry.com/gb:latest
```

## Deploy to Kubernetes

### 1. Create Namespace (optional but recommended)

```bash
kubectl create namespace galactic-bloodshed
```

### 2. Apply the Deployment

```bash
# To default namespace:
kubectl apply -f k8s-deployment.yaml

# Or to custom namespace:
kubectl apply -f k8s-deployment.yaml -n galactic-bloodshed
```

This will:
- Create a ConfigMap with universe generation parameters
- Create a PersistentVolumeClaim for game data
- Create a Deployment with:
  - Init container: `makeuniv` generates the universe once
  - Main container: `gb-server` runs the game
  - Service: Exposes port 8080

### 3. Verify Deployment

```bash
# Check pod status
kubectl get pods
kubectl describe pod gb-server-xxxxx

# Watch init container run
kubectl logs -f gb-server-xxxxx -c makeuniv

# Watch main server start
kubectl logs -f gb-server-xxxxx -c gb-server

# Check service
kubectl get svc gb-server
```

### 4. Access the Server

**Port forward (local access):**
```bash
kubectl port-forward svc/gb-server 8080:8080
```

Then open: http://localhost:8080/enroll

**Ingress (cluster-wide access):**
- The deployment includes an Ingress rule for `gb.local`
- Requires an ingress controller (nginx, traefik, etc.)
- Configure DNS: `gb.local` → your-ingress-ip
- Access: http://gb.local/enroll

**LoadBalancer (cloud providers):**
```bash
kubectl patch svc gb-server -p '{"spec": {"type": "LoadBalancer"}}'
kubectl get svc gb-server  # Get external IP
```

## Configuration

### Adjust Universe Parameters

Edit the ConfigMap in `k8s-deployment.yaml`:

```yaml
data:
  makeuniv-stars: "100"        # Number of stars (1-254)
  makeuniv-minplanets: "3"     # Min planets per system
  makeuniv-maxplanets: "10"    # Max planets per system
```

Then reapply:
```bash
kubectl apply -f k8s-deployment.yaml
kubectl rollout restart deployment/gb-server
```

The init container will regenerate the universe on the next pod restart.

### Storage Options

**Ephemeral (testing):**
```yaml
volumes:
- name: data
  emptyDir: {}  # Data lost when pod restarts
```

**Persistent (production):**
```yaml
volumes:
- name: data
  persistentVolumeClaim:
    claimName: gb-data-pvc
```

The included `k8s-deployment.yaml` uses PersistentVolumeClaim (recommended for production).

## Player Enrollment

### Web UI

1. Port forward or access via ingress: http://localhost:8080/enroll
2. Fill in racial attributes
3. Submit form
4. Get assigned player number

### API Endpoint

```bash
curl -X POST http://localhost:8080/api/enroll \
  -H "Content-Type: application/json" \
  -d '{
    "race_name": "Terrans",
    "password": "secret",
    "info": "Earth humans",
    "home_planet_type": 0,
    "iq": 50,
    "fertilize": 50,
    "birth_rate": 0.5,
    "mass": 1.0,
    "metabolism": 1.0,
    "number_sexes": 2,
    "adventurism": 50,
    "sector_compat": [100, 75, 75, 0, 50, 75, 25, 25]
  }'
```

**Response:**
```json
{
  "success": true,
  "player_num": 1,
  "message": "Race \"Terrans\" enrolled as player 1"
}
```

## Troubleshooting

### Init container stuck in CrashLoopBackOff

```bash
kubectl logs <pod-name> -c makeuniv

# Common issues:
# - Storage volume not provisioned
# - Database file permissions
# - Invalid makeuniv flags
```

### Server not responding

```bash
# Check logs
kubectl logs <pod-name> -c gb-server

# Port forward works?
kubectl port-forward svc/gb-server 8080:8080
curl http://localhost:8080/enroll

# DNS working?
kubectl exec -it <pod-name> -- nslookup gb-server
```

### Data persists but enrollment fails

```bash
# Check database
kubectl exec -it <pod-name> -- ls -la /data

# Check permissions
kubectl exec -it <pod-name> -- stat /data/gb.db

# Restart pod to reinitialize
kubectl delete pod <pod-name>
```

## Scaling Notes

- **Read replicas:** Not currently supported (bbolt is single-writer)
- **Data sharing:** Multiple instances would need shared network storage
- **Recommended:** Keep replicas=1, use load balancing at ingress level for multiple games

## Cleanup

```bash
# Delete deployment (keeps PVC)
kubectl delete deployment gb-server

# Delete everything
kubectl delete -f k8s-deployment.yaml

# Delete namespace
kubectl delete namespace galactic-bloodshed
```

## Next Steps

1. **Integrate WebSocket server** instead of HTTP (for real-time telnet replacement)
2. **Add database replication** for high availability
3. **Setup monitoring** (Prometheus/Grafana)
4. **Add authentication** (OAuth2 integration)
5. **Scale to multi-pod** with shared storage backend
