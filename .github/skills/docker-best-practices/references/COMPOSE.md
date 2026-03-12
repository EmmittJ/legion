# Docker Compose Reference for Legion

## Full docker-compose.yml

```yaml
services:
  dolt:
    image: dolthub/dolt-sql-server:latest
    ports:
      - "3307:3306"
    volumes:
      - dolt-data:/var/lib/dolt
    healthcheck:
      test: ["CMD", "mysqladmin", "ping", "-h", "127.0.0.1", "-P", "3306", "-u", "root"]
      interval: 5s
      timeout: 3s
      retries: 10
      start_period: 10s

  archon:
    build:
      context: .
      dockerfile: cmd/archon/Dockerfile
    environment:
      - DOLT_DSN=mysql://root@dolt:3306/legion
      - REPO_URL=${REPO_URL}
      - VESSEL_IMAGE=legion/vessel-copilot:latest
      - GITHUB_TOKEN=${GITHUB_TOKEN}
    depends_on:
      dolt:
        condition: service_healthy
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    restart: unless-stopped

volumes:
  dolt-data:
```

## Environment Variables

| Variable | Component | Description |
|---|---|---|
| `DOLT_DSN` | Archon | MySQL DSN for Beads database |
| `REPO_URL` | Archon → injected to vessels | Git repo to clone for work |
| `VESSEL_IMAGE` | Archon | Docker image to run for vessel containers |
| `GITHUB_TOKEN` | Archon → injected to vessels | ACP auth for Copilot backend |
| `ISSUE_ID` | Vessel only | Which Beads issue to work on |

## Running Locally

```bash
# Build the vessel image first
docker build -f Dockerfile.vessel-copilot -t legion/vessel-copilot:latest .

# Start the stack
REPO_URL=https://github.com/EmmittJ/legion.git \
GITHUB_TOKEN=$(gh auth token) \
docker compose up --build

# Tear down
docker compose down -v
```

## Debugging

```bash
# Check archon logs
docker compose logs archon -f

# Connect to Dolt directly
mysql -h 127.0.0.1 -P 3307 -u root legion

# Inspect a vessel container that exited
docker ps -a --filter label=legion.issue
docker logs <container-id>
```
