# Container Logging & Troubleshooting

This guide helps you monitor and debug Legion services during development using Docker Compose container logs.

---

## Viewing Container Logs

### All Services

To watch logs from all running containers in real-time:

```bash
docker compose logs -f
```

This streams combined output from Dolt, Archon, and any active vessel containers. Press `Ctrl+C` to stop.

### Specific Services

View logs for a single service:

```bash
# Dolt (database) logs
docker compose logs -f dolt

# Archon (pulse/watcher loop) logs
docker compose logs -f archon

# Vessel driver (if running manually for testing)
docker compose logs -f vessel-driver
```

### View Past Logs (Without Streaming)

To view the last 50 lines without following:

```bash
docker compose logs --tail=50 archon
```

---

## Container Metrics

### Resource Usage

View real-time CPU, memory, and network stats for all containers:

```bash
docker stats
```

### Container Status

List all containers, their IDs, and current status:

```bash
docker compose ps
```

### Example Output

```
NAME                   IMAGE                        STATUS
legion-dolt-1          doltdb/dolt:latest           Up 2 minutes
legion-archon-1        legion/archon:latest         Up 2 minutes
```

---

## Health Checks

### Dolt SQL Server Health

Verify Dolt is accepting connections and responsive:

```bash
docker compose exec dolt dolt sql -q "SELECT 1;"
```

**Expected output:**
```
1
```

### Beads Database Status

Check if Beads data is being written:

```bash
docker compose exec dolt dolt log --oneline | head -5
```

This shows recent commits to the Beads repository inside Dolt.

---

## Common Errors & Where to Find Them

### Dolt Startup Errors

Dolt fails to initialize or crashes on startup:

```bash
docker compose logs dolt | grep -i error
```

**Common causes:**
- Port 3306 already in use
- Insufficient disk space
- Database corruption (try `docker compose down -v` to reset, then restart)

### Archon Pulse Loop Errors

Archon's poll cycle is failing (not picking up ready issues):

```bash
docker compose logs archon | grep -i pulse
```

Or view all errors:

```bash
docker compose logs archon | grep -i error
```

**Common causes:**
- `REPO_URL` or `GITHUB_TOKEN` not set
- `bd` CLI not installed or not on `PATH` inside container
- Beads repo not initialized (`bd init` not run)

### Vessel Spawn Errors

Archon cannot spawn a vessel container for a ready issue:

```bash
docker compose logs archon | grep -i spawn
```

Or search for Docker errors:

```bash
docker compose logs archon | grep -i "docker"
```

**Common causes:**
- Vessel image not built (`docker compose build` not run)
- Docker socket not accessible
- Previous vessel container didn't clean up (run `docker compose ps -a` and remove stale containers)

### Vessel Driver / ACP Errors

The vessel is running but failing during the Copilot session:

```bash
# View vessel's stderr/stdout
docker compose logs archon | grep -i "vessel"

# Or check the vessel container directly if still running
docker logs <vessel-container-id> | grep -i error
```

---

## Debug Workflow

### Terminal 1: Start Services

Start Dolt and Archon, watch their startup logs:

```bash
docker compose up
```

Watch for:
- Dolt reporting it's listening on port 3306
- Archon reporting "Starting pulse loop" without errors
- Both services reporting healthy status

### Terminal 2: Monitor Archon Loop

In a second terminal, follow Archon's pulse/watcher cycles:

```bash
docker compose logs -f archon
```

Watch for:
- `pulse: checking for ready issues` every 5 seconds
- `pulse: found issue <id>`, `spawning vessel...`
- `watcher: checking active vessels` every 10 seconds
- `watcher: vessel exited with status...`

### Terminal 3: Create and Monitor an Issue

In a third terminal, file a test task and monitor it:

```bash
# Create a test issue
lg invoke "Test issue for smoke test"
# Output: Created issue: legion-abc123

# Check its status
lg status

# Watch Archon pick it up (go back to Terminal 2)
# Once complete, read the vessel's trace
lg log legion-abc123
```

### Full Example Walkthrough

1. **Terminal 1** — Start services:
   ```bash
   docker compose up
   # Wait for both services to be healthy (watch for startup messages)
   ```

2. **Terminal 2** — Monitor Archon:
   ```bash
   docker compose logs -f archon
   # You'll see "Starting pulse loop" and then periodic checks
   ```

3. **Terminal 3** — File a task:
   ```bash
   lg invoke "Add logging to health check endpoint"
   # Created issue: legion-4ab
   
   # Check status every few seconds
   lg status
   
   # Once the vessel completes (you'll see it in Terminal 2's logs):
   lg log legion-4ab
   ```

---

## Useful Log Filtering

### Show Only Errors

```bash
docker compose logs | grep -i error
```

### Show Logs for a Specific Time Range

View logs from the last 10 minutes:

```bash
docker compose logs --since=10m
```

### Count Error Frequency

See how many errors occurred in Archon:

```bash
docker compose logs archon | grep -ic error
```

### Watch a Specific Pattern

Follow logs matching a keyword in real-time:

```bash
docker compose logs -f archon | grep -i "spawn"
```

---

## Cleanup & Reset

### View Running Containers

```bash
docker compose ps
```

### Stop All Services

```bash
docker compose down
```

### Reset Database (Warning: Deletes all data)

```bash
docker compose down -v
docker compose up -d dolt
# Reinitialize Beads
docker compose exec dolt bd init
```

### Remove Stale Vessel Containers

```bash
docker container ls -a | grep vessel
docker container rm <container-id>
```

---

## Additional Resources

- **Docker Compose Docs:** https://docs.docker.com/compose/
- **Beads CLI:** `bd --help`
- **Legion Architecture:** See `README.md` for system overview
- **Current Goals:** See `docs/ROADMAP.md` for current goals

---

## Issues or Questions?

If logs don't provide clarity:

1. Ensure all environment variables are set:
   ```bash
   env | grep -E "REPO_URL|GITHUB_TOKEN"
   ```

2. Confirm Beads is initialized:
   ```bash
   docker compose exec dolt bd list
   ```

3. Check docker-compose.yml for service configuration issues.

4. Review the main README for configuration requirements and quick start.
