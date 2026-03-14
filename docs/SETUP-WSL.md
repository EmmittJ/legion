# WSL Setup Guide for Legion Test Harness

The Legion test harness (`tests/spawn-cycle.sh`) is a bash script that validates the Archon pulse loop and vessel lifecycle on a full Docker Compose stack. To run tests on Windows, you need **Windows Subsystem for Linux (WSL2)** with the required CLI tools installed.

This guide covers:
- Prerequisites (WSL2, Docker, Go)
- Installing `bd` (Beads CLI) and `dolt` in WSL
- Running the test harness
- Troubleshooting common issues

---

## Prerequisites

### 1. WSL2 Installed

WSL2 (not WSL1) is required for proper Docker integration.

**Check if WSL is installed:**
```powershell
# PowerShell (Windows)
wsl --version
# Expected output: WSL version: 2.x.x
```

**Install WSL2 if needed:**
```powershell
# PowerShell (Admin)
wsl --install
# This installs WSL2 and Ubuntu 22.04 by default
# You can also specify: wsl --install -d Ubuntu-20.04
```

See [Microsoft's WSL installation guide](https://learn.microsoft.com/en-us/windows/wsl/install) for detailed steps.

### 2. Docker Desktop Configured for WSL Backend

Docker Desktop must use the WSL2 backend to expose the Docker socket to your WSL environment.

**Check Docker integration:**
1. Open **Docker Desktop**
2. Go to **Settings → Resources → WSL integration**
3. Enable integration with your WSL distro (e.g., "Ubuntu")
4. Click **Apply & Restart**

**Verify in WSL:**
```bash
# WSL terminal
docker --version
# Expected: Docker version 24.x.x (or similar)
```

### 3. Go 1.21+

Legion binaries are built with Go. You need Go 1.21 or later in your WSL environment.

**Check if Go is installed:**
```bash
# WSL terminal
go version
# Expected: go version go1.21.x linux/amd64
```

**Install Go if needed:**
```bash
# WSL terminal
wget https://go.dev/dl/go1.21.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.21.0.linux-amd64.tar.gz
# Add to ~/.bashrc or ~/.zshrc if not already present:
# export PATH="$PATH:/usr/local/go/bin"
source ~/.bashrc
go version
```

---

## Installation Steps (in WSL Terminal)

### Step 1: Update System Packages

```bash
# WSL terminal
sudo apt-get update && sudo apt-get upgrade -y
```

### Step 2: Install Base Dependencies

```bash
# WSL terminal
sudo apt-get install -y \
  git \
  curl \
  ca-certificates \
  jq \
  build-essential
```

### Step 3: Install Beads CLI (`bd`)

Beads is the issue and trace store that Legion uses.

1. **Download the latest release:**
   ```bash
   # WSL terminal
   # Visit https://github.com/steveyegge/beads/releases to find the latest version
   wget https://github.com/steveyegge/beads/releases/download/v0.x.x/bd-linux-amd64
   ```

2. **Make it executable and move to PATH:**
   ```bash
   # WSL terminal
   chmod +x bd-linux-amd64
   sudo mv bd-linux-amd64 /usr/local/bin/bd
   ```

3. **Verify installation:**
   ```bash
   # WSL terminal
   bd --version
   # Expected: beads version X.X.X
   ```

   **Troubleshooting:** If `command not found`, see [Troubleshooting: bd not in PATH](#troubleshooting).

### Step 4: Install Dolt CLI

Dolt is the version control system for data that Beads uses as a backend.

1. **Follow the official DoltHub setup guide:**
   ```bash
   # WSL terminal
   # Install via curl (recommended for WSL):
   curl https://install.dolthub.com.sh | bash
   ```
   
   For other installation methods, see [Dolt CLI Setup Guide](https://docs.dolthub.com/reference/cli/cli-setup).

2. **Verify installation:**
   ```bash
   # WSL terminal
   dolt version
   # Expected: dolt version X.X.X
   ```

   **Troubleshooting:** If `command not found`, see [Troubleshooting: dolt not in PATH](#troubleshooting).

### Step 5: Clone or Access the Legion Repository

Choose one approach:

#### Option A: Clone inside WSL (Recommended)

```bash
# WSL terminal
cd ~/projects  # or your preferred directory
git clone https://github.com/EmmittJ/legion.git
cd legion
```

#### Option B: Work from Windows via `/mnt/c`

If you cloned the repo on Windows (e.g., `C:\Users\YourUser\projects\legion`), you can access it from WSL:

```bash
# WSL terminal
cd /mnt/c/Users/YourUser/projects/legion
# This path is slower than WSL-native paths; use Option A for better performance
```

### Step 6: Initialize Beads Repository (One Time)

Legion requires an initialized Beads repo in the project root:

```bash
# WSL terminal (from legion root)
bd init
# This creates .beads/ directory with the Dolt database
```

### Step 7: Verify All Tools

```bash
# WSL terminal (from legion root)
echo "Beads version:"
bd --version

echo "Dolt version:"
dolt version

echo "Docker version:"
docker --version

echo "Go version:"
go version

echo "Git version:"
git --version
```

All five commands should succeed. If any fail, see [Troubleshooting](#troubleshooting).

---

## Running the Test Harness

### Start the Legion Stack

```bash
# WSL terminal (from legion root)
docker compose up -d
# Starts: Dolt, Archon, and supporting services
```

### Run the Quick Test

```bash
# WSL terminal (from legion root)
bash tests/spawn-cycle.sh
```

**Expected output:**
```
═══════════════════════════════════════════════════════════════
MVP.1 Spawn Cycle Validation Test
═══════════════════════════════════════════════════════════════
[12:34:56] Checking prerequisites...
[12:34:56] Creating test issue via lg invoke...
[PASS] Issue created: legion-abc123
[12:34:57] Waiting for Archon to pick up issue...
[PASS] Issue marked in_progress by Archon
[12:34:58] Waiting for vessel container to complete...
[PASS] Issue marked closed (vessel completed successfully)
[12:35:10] Fetching traces for issue...
[PASS] Traces contain expected markers
[PASS] MVP.1 Spawn Cycle Test PASSED
═══════════════════════════════════════════════════════════════
Issue ID: legion-abc123
Logs: .test-logs
```

**Test logs are saved to `.test-logs/`:**
- `test-{TIMESTAMP}.log` — Full test output
- `archon-{TIMESTAMP}.log` — Archon service logs
- `docker-compose-{TIMESTAMP}.log` — All container logs

### View Test Logs

```bash
# WSL terminal (from legion root)
ls -la .test-logs/
tail -f .test-logs/test-*.log  # Watch latest logs
```

---

## Troubleshooting

### `bd: command not found`

**Problem:** Beads CLI is not in your PATH.

**Solution:**

1. **Check if `bd` is installed:**
   ```bash
   ls -la /usr/local/bin/bd
   ```

2. **If file exists but not in PATH, add to `.bashrc` or `.zshrc`:**
   ```bash
   # Append to ~/.bashrc
   echo 'export PATH="/usr/local/bin:$PATH"' >> ~/.bashrc
   source ~/.bashrc
   
   # Then verify:
   bd --version
   ```

3. **If file doesn't exist, re-download and install:**
   ```bash
   wget https://github.com/steveyegge/beads/releases/download/v0.x.x/bd-linux-amd64
   chmod +x bd-linux-amd64
   sudo mv bd-linux-amd64 /usr/local/bin/bd
   bd --version
   ```

### `dolt: command not found`

**Problem:** Dolt CLI is not installed or not in PATH.

**Solution:**

1. **Verify it was installed in the correct WSL distro:**
   ```bash
   which dolt
   ```

2. **If not found, reinstall via curl:**
   ```bash
   curl https://install.dolthub.com.sh | bash
   # Follow prompts to add to PATH
   source ~/.bashrc
   dolt version
   ```

3. **If you installed Dolt on Windows only, WSL won't see it:**
   - Dolt must be installed **inside WSL**, not on the Windows host
   - Run the installation command above in your WSL terminal

### `docker: permission denied while trying to connect to Docker daemon`

**Problem:** Your WSL user doesn't have permission to access the Docker socket.

**Solution:**

1. **Add your WSL user to the `docker` group:**
   ```bash
   sudo usermod -aG docker $USER
   ```

2. **Apply the new group membership (pick one):**
   ```bash
   # Option A: Log out and back in (close and reopen WSL terminal)
   # Option B: Run immediately in current shell:
   newgrp docker
   ```

3. **Verify:**
   ```bash
   docker --version
   ```

### File path issues: `No such file or directory`

**Problem:** Paths on `/mnt/c/...` are slow; `docker compose` can't find files.

**Solution:**

1. **Clone the repo inside WSL:**
   ```bash
   # Option A (Recommended): Fast native WSL path
   cd ~/projects
   git clone https://github.com/EmmittJ/legion.git
   cd legion
   ```

2. **If you must use Windows paths, symlink to a WSL-native location:**
   ```bash
   # Option B: Link from /mnt/c to home directory
   ln -s /mnt/c/Users/YourUser/projects/legion ~/legion
   cd ~/legion
   ```

3. **Run Docker Compose from the WSL-native location:**
   ```bash
   docker compose up -d
   ```

### `docker compose up` hangs or fails to start services

**Problem:** Docker Desktop WSL integration is not working.

**Solution:**

1. **Check Docker Desktop is running:**
   - Look for the Docker whale icon in your system tray (Windows)
   - If not running, start Docker Desktop

2. **Check WSL integration is enabled:**
   - Docker Desktop → Settings → Resources → WSL integration
   - Toggle on for your distro (Ubuntu, etc.)
   - Click "Apply & Restart"

3. **Verify Docker socket is accessible:**
   ```bash
   # WSL terminal
   ls -la /var/run/docker.sock
   # Should show: srwxrwxrwx  docker docker
   ```

4. **Restart Docker and try again:**
   ```bash
   # WSL terminal
   docker compose down
   docker compose up -d
   ```

### Test script fails: `Not at legion repo root`

**Problem:** Script is run from a directory that doesn't contain `MVP.md`.

**Solution:**

1. **Make sure you're in the legion repo root:**
   ```bash
   cd ~/projects/legion  # or your legion directory
   pwd  # Verify current directory
   ls MVP.md  # Should exist
   ```

2. **Run the test:**
   ```bash
   bash tests/spawn-cycle.sh
   ```

### Test times out: `Timeout waiting for spawn` or `Timeout waiting for completion`

**Problem:** Archon didn't pick up the issue, or the vessel container is stuck.

**Solution:**

1. **Check if the Docker stack is running:**
   ```bash
   docker compose ps
   # All services should show "Up"
   ```

2. **Increase the timeout:**
   ```bash
   # WSL terminal
   TIMEOUT_SPAWN=120 TIMEOUT_COMPLETE=600 bash tests/spawn-cycle.sh
   ```

3. **Enable debug output:**
   ```bash
   # WSL terminal
   DEBUG=1 bash tests/spawn-cycle.sh
   # Prints extra logs for diagnosis
   ```

4. **Check Archon logs:**
   ```bash
   docker compose logs -f archon
   # Look for errors or startup issues
   ```

5. **Check Dolt is healthy:**
   ```bash
   docker compose logs dolt
   # Should show "ready to serve"
   ```

---

## Quick Reference

### Common Commands

```bash
# Start the Legion stack
docker compose up -d

# View logs
docker compose logs -f archon
docker compose logs -f dolt

# Stop the stack
docker compose down

# Run test harness
bash tests/spawn-cycle.sh

# Run with debug output
DEBUG=1 bash tests/spawn-cycle.sh

# Build Legion binaries (from legion root)
go build -o archon.exe        ./cmd/archon
go build -o vessel-driver.exe ./cmd/vessel-driver
go build -o lg.exe            ./cmd/lg
```

### Verify Environment

```bash
# Check all tools are installed
bd --version
dolt version
docker --version
go version
git --version
```

---

## Next Steps

Once you have the test harness running:

1. **Read the test script:** `tests/spawn-cycle.sh` documents what each validation step does
2. **Read MVP.md:** `MVP.md` defines the success criteria for the Archon spawn cycle
3. **Review integration tests:** `tests/` contains other test suites you can explore
4. **Check the troubleshooting guide:** `docs/TROUBLESHOOTING.md` for runtime issues

For detailed information on Legion architecture and configuration, see the [main README](../README.md).
