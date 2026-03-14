<#
.SYNOPSIS
    smoke-vessel.ps1 — Run a vessel container manually for a single issue, streaming
    output live. No full docker compose restart required — just a running dolt service
    on the legion_legion-net network.

.DESCRIPTION
    Loads .env from the repo root, spins up a vessel container against an existing
    Beads issue, and streams its output directly to the terminal. Useful for:
      - Debugging a specific issue without waiting for Archon to pick it up
      - Verifying the vessel image boots and connects to dolt correctly
      - Smoke-testing vessel-driver behaviour after an image rebuild

    The container is named smoke-vessel-<timestamp> to avoid conflicts with other
    running tests or Archon-spawned vessels. It is removed automatically on exit
    (--rm flag).

.PARAMETER IssueId
    Beads issue ID to pass as ISSUE_ID into the vessel. Default: lg-20v

.PARAMETER VesselImage
    Override the vessel image. Falls back to VESSEL_IMAGE in .env, then
    legion/vessel-copilot:latest.

.PARAMETER Network
    Docker network to attach to. Default: legion_legion-net

.PARAMETER DoltHost
    Hostname of the dolt service on the network. Default: dolt

.PARAMETER DoltPort
    Port the dolt SQL server listens on. Default: 3306

.EXAMPLE
    # Run against default issue lg-20v:
    .\tests\smoke-vessel.ps1

.EXAMPLE
    # Run against a specific issue:
    .\tests\smoke-vessel.ps1 -IssueId lg-abc

.EXAMPLE
    # Override image:
    .\tests\smoke-vessel.ps1 -IssueId lg-abc -VesselImage legion/vessel-copilot:dev

.NOTES
    Requirements:
      - Docker installed and running
      - dolt container reachable on legion_legion-net (docker compose up dolt -d)
      - .env file at repo root with at minimum GITHUB_TOKEN set
      - Vessel image built: docker compose build (or docker build -f Dockerfile.vessel-copilot)

    Exit Codes:
      0 — Container exited cleanly (exit code 0 from vessel-driver)
      1 — Pre-flight check failed or container exited non-zero
#>

param(
    [string]$IssueId    = "lg-20v",
    [string]$VesselImage = "",
    [string]$Network    = "legion_legion-net",
    [string]$DoltHost   = "dolt",
    [int]   $DoltPort   = 3306
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# ─────────────────────────────────────────────────────────────────────────────
# Paths
# ─────────────────────────────────────────────────────────────────────────────

$repoRoot  = Split-Path -Parent $PSScriptRoot
$envFile   = Join-Path $repoRoot ".env"
$timestamp = Get-Date -Format "yyyyMMdd_HHmmss"
$containerName = "smoke-vessel-$timestamp"

# ─────────────────────────────────────────────────────────────────────────────
# Colour helpers
# ─────────────────────────────────────────────────────────────────────────────

function Write-Header  { param([string]$m) Write-Host $m -ForegroundColor Cyan   }
function Write-Info    { param([string]$m) Write-Host "  $m" -ForegroundColor White  }
function Write-Ok      { param([string]$m) Write-Host "  [OK]  $m" -ForegroundColor Green  }
function Write-Warn    { param([string]$m) Write-Host "  [WARN] $m" -ForegroundColor Yellow }
function Write-Err     { param([string]$m) Write-Host "  [ERR] $m" -ForegroundColor Red    }
function Write-Divider { Write-Host ("─" * 66) -ForegroundColor DarkGray }

# ─────────────────────────────────────────────────────────────────────────────
# Load .env
# ─────────────────────────────────────────────────────────────────────────────

function Import-DotEnv {
    param([string]$Path)

    if (-not (Test-Path $Path)) {
        Write-Err ".env not found at: $Path"
        Write-Err "Copy .env.example to .env and fill in GITHUB_TOKEN at minimum."
        exit 1
    }

    $vars = @{}
    $lineNo = 0

    foreach ($line in Get-Content $Path) {
        $lineNo++
        # Skip blank lines and comments
        if ($line -match '^\s*$' -or $line -match '^\s*#') { continue }

        if ($line -match '^([A-Za-z_][A-Za-z0-9_]*)=(.*)$') {
            $key   = $matches[1]
            $value = $matches[2].Trim()

            # Strip surrounding quotes if present
            if ($value -match '^"(.*)"$' -or $value -match "^'(.*)'$") {
                $value = $matches[1]
            }

            # Only store non-empty values
            if ($value -ne "") {
                $vars[$key] = $value
            }
        }
    }

    return $vars
}

# ─────────────────────────────────────────────────────────────────────────────
# Pre-flight checks
# ─────────────────────────────────────────────────────────────────────────────

function Assert-Docker {
    try {
        $null = docker info 2>&1
        if ($LASTEXITCODE -ne 0) { throw }
        Write-Ok "Docker daemon reachable"
    } catch {
        Write-Err "Docker is not running or not installed."
        exit 1
    }
}

function Assert-NetworkExists {
    param([string]$Name)

    $networks = docker network ls --format "{{.Name}}" 2>&1
    if ($networks -contains $Name) {
        Write-Ok "Network '$Name' exists"
    } else {
        Write-Warn "Network '$Name' not found — container will fail to attach."
        Write-Warn "Is the stack up?  docker compose up dolt -d"
        # Don't hard-exit: let docker run surface the real error message
    }
}

function Assert-ImageExists {
    param([string]$Image)

    $images = docker images --format "{{.Repository}}:{{.Tag}}" 2>&1
    # Also check without explicit :latest tag
    $imageBase = $Image -replace ':latest$', ''
    $found = $images | Where-Object { $_ -eq $Image -or $_ -eq "${imageBase}:latest" }

    if ($found) {
        Write-Ok "Image '$Image' found locally"
    } else {
        Write-Warn "Image '$Image' not found locally — docker run will attempt to pull."
        Write-Warn "Build it first:  docker compose build"
    }
}

# ─────────────────────────────────────────────────────────────────────────────
# Main
# ─────────────────────────────────────────────────────────────────────────────

Write-Divider
Write-Header "  Legion Vessel Smoke Test"
Write-Header "  Issue   : $IssueId"
Write-Header "  Started : $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')"
Write-Header "  Container: $containerName"
Write-Divider
Write-Host ""

# 1. Load .env
Write-Info "Loading $envFile ..."
$env = Import-DotEnv -Path $envFile

# Required: GITHUB_TOKEN
if (-not $env.ContainsKey("GITHUB_TOKEN")) {
    Write-Err "GITHUB_TOKEN is missing from .env — cannot run without it."
    exit 1
}
Write-Ok "GITHUB_TOKEN loaded"

# Required: REPO_URL
if (-not $env.ContainsKey("REPO_URL")) {
    Write-Err "REPO_URL is missing from .env."
    exit 1
}
Write-Ok "REPO_URL = $($env['REPO_URL'])"

# Resolve vessel image: CLI flag → .env → default
if ($VesselImage -eq "") {
    if ($env.ContainsKey("VESSEL_IMAGE") -and $env["VESSEL_IMAGE"] -ne "") {
        $VesselImage = $env["VESSEL_IMAGE"]
    } else {
        $VesselImage = "legion/vessel-copilot:latest"
    }
}
Write-Ok "Vessel image: $VesselImage"

# Resolve VESSEL_MODEL: .env → default
$vesselModel = "gpt-5-mini"
if ($env.ContainsKey("VESSEL_MODEL") -and $env["VESSEL_MODEL"] -ne "") {
    $vesselModel = $env["VESSEL_MODEL"]
}
Write-Ok "Model: $vesselModel"

# Optional VESSEL_TIMEOUT
$vesselTimeout = ""
if ($env.ContainsKey("VESSEL_TIMEOUT") -and $env["VESSEL_TIMEOUT"] -ne "") {
    $vesselTimeout = $env["VESSEL_TIMEOUT"]
}

Write-Host ""

# 2. Pre-flight
Write-Info "Running pre-flight checks..."
Assert-Docker
Assert-NetworkExists -Name $Network
Assert-ImageExists   -Name $VesselImage

Write-Host ""

# 3. Build docker run args
$runArgs = @(
    "run",
    "--rm",
    "--name", $containerName,
    "--network", $Network,
    "-e", "ISSUE_ID=$IssueId",
    "-e", "REPO_URL=$($env['REPO_URL'])",
    "-e", "GITHUB_TOKEN=$($env['GITHUB_TOKEN'])",
    "-e", "VESSEL_MODEL=$vesselModel",
    "-e", "DOLT_HOST=$DoltHost",
    "-e", "DOLT_PORT=$DoltPort"
)

# Inject optional env vars only when non-empty
if ($vesselTimeout -ne "") {
    $runArgs += "-e", "VESSEL_TIMEOUT=$vesselTimeout"
}

$runArgs += $VesselImage

# 4. Print the command (mask token)
$displayArgs = $runArgs | ForEach-Object {
    if ($_ -match "^GITHUB_TOKEN=") { "GITHUB_TOKEN=***" } else { $_ }
}
Write-Divider
Write-Info "docker $($displayArgs -join ' ')"
Write-Divider
Write-Host ""

# 5. Run — streaming output live (no -d)
Write-Header "  Vessel output:"
Write-Divider

$proc = Start-Process `
    -FilePath "docker" `
    -ArgumentList $runArgs `
    -NoNewWindow `
    -PassThru `
    -Wait

$exitCode = $proc.ExitCode

# 6. Summary
Write-Host ""
Write-Divider
if ($exitCode -eq 0) {
    Write-Ok "Container exited cleanly (exit code 0)"
    Write-Header "  Smoke test PASSED for issue: $IssueId"
} else {
    Write-Err "Container exited with code $exitCode"
    Write-Err "Smoke test FAILED for issue: $IssueId"
    Write-Err "Check the output above, or pull logs:"
    Write-Err "  docker logs $containerName   (if --rm hasn't cleaned it yet)"
    Write-Err "  docker compose logs dolt      (if dolt connectivity was the issue)"
}
Write-Divider

exit $exitCode
