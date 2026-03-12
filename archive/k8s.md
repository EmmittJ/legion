# K8s Infrastructure

## 1. Archon Deployment

Archon is a persistent daemon that lives in your cluster. It doesn't do the heavy lifting; it simply watches Grimoire and commands the K8s API to manifest the Many.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: legion-archon
  labels:
    app: legion
    role: controller
spec:
  replicas: 1
  selector:
    matchLabels:
      app: legion
  template:
    metadata:
      labels:
        app: legion
    spec:
      serviceAccountName: legion-archon-sa # Needs permissions to create Jobs
      containers:
        - name: archon
          image: legion/archon:latest
          env:
            - name: GRIMOIRE_PATH
              value: "/data/grimoire"
          volumeMounts:
            - name: grimoire-storage
              mountPath: /data/grimoire
      volumes:
        - name: grimoire-storage
          persistentVolumeClaim:
            claimName: grimoire-pvc
```

## 2. Vessel Base Image (Dockerfile)

Every Wraith, Hierophant, and Gauntlet uses this base. It includes the tools required to interact with both the code (Git) and the ledger (Beads).

```dockerfile
FROM python:3.11-slim

# Install forge tools
RUN apt-get update && apt-get install -y \
    git \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Install Beads and Legion runtime
RUN pip install beads-cli kubernetes openai

WORKDIR /vessel

# Entrypoint handles Vessel possession logic:
# 1. Pull Molecule context from Grimoire
# 2. Checkout Shadow Branch
# 3. Execute task (Wisp generation)
# 4. Push and terminate
COPY entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/bin/bash", "/entrypoint.sh"]
```

## 3. Local Scale Test (Docker Compose)

To test the Molecule/Wisp interaction locally before pushing to a cloud K8s cluster, use this `docker-compose.yaml`. It stands up a Dolt database (Grimoire's engine) and Archon.

```yaml
services:
  grimoire-db:
    image: dolthub/dolt-sql-server:latest
    ports:
      - "3306:3306"
    environment:
      - DOLT_ROOT_PATH=/var/lib/dolt

  archon:
    build: .
    volumes:
      - .:/app
      - /var/run/docker.sock:/var/run/docker.sock # Allows Archon to spawn siblings
    environment:
      - GRIMOIRE_CONNECTION=mysql://root@grimoire-db:3306/legion
    depends_on:
      - grimoire-db
```

## 4. First Ritual (Testing Scale)

Once running, test the "We are many" philosophy by manually injecting 10 `READY` Molecules into the database:

- Archon will detect all 10 simultaneously.
- It will fire 10 Vessel containers.
- 10 distinct Shadow Branches will appear in Git history.
- Each branch will contain Wisps (commits) showing agent progress.

## 5. Next Step: Identity Registry

To make Archon actually smart, it needs an Identity Registry — a configuration that maps Molecule types to Docker images and System Prompts (Pacts). This is defined in `identities.yaml`.
