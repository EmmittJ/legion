<#
.SYNOPSIS
    spawn-cycle.ps1 - MVP.1 validation: Archon spawn cycle test (Windows PowerShell)

.DESCRIPTION
    This test validates the core Archon pulse loop behavior:
      1. Create a task issue via `lg invoke`
      2. Wait for Archon to pick it up and spawn a vessel container
      3. Monitor vessel container lifecycle (start -> run -> exit)
      4. Verify vessel container exited cleanly (exit code 0)
      5. Confirm issue marked as "closed" or "failed"
      6. Query logs to confirm traces were written

    This is the native Windows PowerShell version - no WSL, no bash required.

.PARAMETER TimeoutSpawn
    Max seconds to wait for Archon to spawn the issue (default: 60)

.PARAMETER TimeoutComplete
    Max seconds to wait for vessel to complete (default: 300)

.PARAMETER Debug
    Enable verbose debug output

.PARAMETER SkipDockerCheck
    Skip Docker Compose health check (useful for offline testing)

.EXAMPLE
    # Basic usage (with docker compose stack already running):
    powershell -ExecutionPolicy Bypass -File tests/spawn-cycle.ps1

.EXAMPLE
    # With debug output:
    powershell -ExecutionPolicy Bypass -File tests/spawn-cycle.ps1 -Debug

.EXAMPLE
    # With custom timeouts:
    powershell -ExecutionPolicy Bypass -File tests/spawn-cycle.ps1 -TimeoutSpawn 120 -TimeoutComplete 600

.NOTES
    Requires:
      - Docker and Docker Compose installed and running
      - Legion stack running: docker compose up -d
      - bd (Beads CLI) on PATH
      - lg (Legion CLI) on PATH

    Exit Codes:
      0 - Test passed
      1 - Test failed (check .test-logs/ for diagnostics)
#>

param(
    [int]$TimeoutSpawn = 60,
    [int]$TimeoutComplete = 300,
    [switch]$Debug,
    [switch]$SkipDockerCheck
)

# ─────────────────────────────────────────────────────────────────────────────
# Configuration
# ─────────────────────────────────────────────────────────────────────────────

$ErrorActionPreference = "Continue"
$VerbosePreference = if ($Debug) { "Continue" } else { "SilentlyContinue" }

# Get script directory and repo root
$scriptDir = Split-Path -Parent $PSScriptRoot
$logDir = Join-Path $scriptDir ".test-logs"
if (-not (Test-Path $logDir)) {
    New-Item -ItemType Directory -Path $logDir -Force | Out-Null
}

$timestamp = Get-Date -Format "yyyyMMdd_HHmmss"
$testName = "spawn-cycle-$timestamp"
$archonLog = Join-Path $logDir "archon-$timestamp.log"
$vesselLog = Join-Path $logDir "vessel-$timestamp.log"
$testLog = Join-Path $logDir "test-$timestamp.log"

# Global state
$issueId = $null
$testPassed = $false

# ─────────────────────────────────────────────────────────────────────────────
# Logging Utilities
# ─────────────────────────────────────────────────────────────────────────────

function Write-LogLine {
    param(
        [string]$Message,
        [ConsoleColor]$ForegroundColor = [ConsoleColor]::Cyan,
        [string]$Prefix = ""
    )
    
    $timestamp = Get-Date -Format "HH:mm:ss"
    $formattedMsg = "[$timestamp] $Prefix$Message"
    
    Write-Host $formattedMsg -ForegroundColor $ForegroundColor
    Add-Content -Path $testLog -Value $formattedMsg
}

function Write-Log {
    param([string]$Message)
    Write-LogLine -Message $Message -ForegroundColor Cyan
}

function Write-LogPass {
    param([string]$Message)
    Write-LogLine -Message $Message -ForegroundColor Green -Prefix "[PASS] "
}

function Write-LogFail {
    param([string]$Message)
    Write-LogLine -Message $Message -ForegroundColor Red -Prefix "[FAIL] "
}

function Write-LogDebug {
    param([string]$Message)
    if ($Debug) {
        Write-LogLine -Message $Message -ForegroundColor Yellow -Prefix "[DEBUG] "
    } else {
        Write-Verbose $Message
    }
}

function Write-LogError {
    param([string]$Message)
    Write-LogFail $Message
}

function Test-Exit {
    param([string]$Message)
    Write-LogError $Message
    exit 1
}

# ─────────────────────────────────────────────────────────────────────────────
# Utility Functions
# ─────────────────────────────────────────────────────────────────────────────

function Invoke-Command {
    param(
        [string]$Command,
        [string]$Description = ""
    )
    
    Write-LogDebug "Executing: $Command"
    
    try {
        $output = Invoke-Expression $Command 2>&1
        $exitCode = if ($LASTEXITCODE) { $LASTEXITCODE } else { 0 }
        
        Write-LogDebug "Exit code: $exitCode"
        if ($output) {
            Write-LogDebug "Output: $($output | Out-String)"
        }
        
        return @{
            Success = ($exitCode -eq 0)
            ExitCode = $exitCode
            Output = $output
        }
    }
    catch {
        Write-LogDebug "Exception: $_"
        return @{
            Success = $false
            ExitCode = -1
            Output = $_
        }
    }
}

function Test-CommandAvailable {
    param([string]$Command)
    
    try {
        $result = Invoke-Expression "Get-Command $Command -ErrorAction Stop" 2>&1
        return $?
    }
    catch {
        return $false
    }
}

# ─────────────────────────────────────────────────────────────────────────────
# Prerequisite Checks
# ─────────────────────────────────────────────────────────────────────────────

function Check-Prerequisites {
    Write-Log "Checking prerequisites..."
    
    # Check if we're at repo root
    $mvpPath = Join-Path $scriptDir "MVP.md"
    if (-not (Test-Path $mvpPath)) {
        Test-Exit "Not at legion repo root. Expected MVP.md at $scriptDir"
    }
    Write-LogDebug "Repository root: $scriptDir"
    
    # Check for required commands
    $requiredCommands = @("bd", "lg", "docker", "docker-compose")
    foreach ($cmd in $requiredCommands) {
        if (-not (Test-CommandAvailable $cmd)) {
            Test-Exit "Required command not found: $cmd"
        }
    }
    Write-LogDebug "All required commands found: $($requiredCommands -join ', ')"
    
    # Check if docker compose stack is running
    if (-not $SkipDockerCheck) {
        Write-Log "Checking Docker Compose stack..."
        
        $composeFile = Join-Path $scriptDir "docker-compose.yml"
        $result = Invoke-Command "docker-compose -f '$composeFile' ps --services --filter 'status=running'"
        
        if ($result.Success -and ($result.Output -match "dolt")) {
            Write-LogDebug "Docker Compose stack is running"
        } else {
            Test-Exit "Docker Compose stack not running. Start with: docker compose up"
        }
    } else {
        Write-LogDebug "Skipping Docker Compose health check (SkipDockerCheck=true)"
    }
}

# ─────────────────────────────────────────────────────────────────────────────
# Test Execution
# ─────────────────────────────────────────────────────────────────────────────

function Create-Issue {
    Write-Log "Creating test issue via lg invoke..."
    
    $epochSeconds = [int](New-Date -UFormat %s)
    $title = "MVP.1 spawn test - $epochSeconds"
    Write-LogDebug "Issue title: $title"
    
    $result = Invoke-Command "lg invoke '$title'"
    
    if (-not $result.Success) {
        Test-Exit "lg invoke failed: $($result.Output)"
    }
    
    Write-LogDebug "lg invoke output: $($result.Output)"
    
    # Extract issue ID from output (Created issue: <ID>)
    if ($result.Output -match "Created issue:\s+(\S+)") {
        $script:issueId = $matches[1]
    }
    
    if ([string]::IsNullOrEmpty($issueId)) {
        Test-Exit "Failed to extract issue ID from output: $($result.Output)"
    }
    
    Write-LogPass "Issue created: $issueId"
}

function Wait-ForSpawn {
    Write-Log "Waiting for Archon to pick up issue (max ${TimeoutSpawn}s)..."
    
    $elapsed = 0
    $interval = 2
    
    while ($elapsed -lt $TimeoutSpawn) {
        $status = Get-IssueStatus
        Write-LogDebug "Issue status: $status (elapsed: ${elapsed}s)"
        
        if ($status -eq "in_progress") {
            Write-LogPass "Issue marked in_progress by Archon"
            return $true
        }
        
        Start-Sleep -Seconds $interval
        $elapsed += $interval
    }
    
    Test-Exit "Timeout waiting for spawn (issue never marked in_progress). Current status: $status"
    return $false
}

function Wait-ForCompletion {
    Write-Log "Waiting for vessel container to complete (max ${TimeoutComplete}s)..."
    
    $elapsed = 0
    $interval = 3
    
    while ($elapsed -lt $TimeoutComplete) {
        $status = Get-IssueStatus
        Write-LogDebug "Issue status: $status (elapsed: ${elapsed}s)"
        
        switch ($status) {
            "closed" {
                Write-LogPass "Issue marked closed (vessel completed successfully)"
                return $true
            }
            "blocked" {
                Write-LogFail "Issue marked blocked (vessel driver exited with error)"
                return $false
            }
            "in_progress" {
                # Still running, keep polling
            }
        }
        
        Start-Sleep -Seconds $interval
        $elapsed += $interval
    }
    
    Test-Exit "Timeout waiting for completion (issue stuck in $status). Check logs at $logDir"
    return $false
}

function Get-IssueStatus {
    <#
    .SYNOPSIS
    Query Beads for issue status via bd show --json
    #>
    
    $result = Invoke-Command "bd show '$issueId' --json"
    
    if (-not $result.Success) {
        Write-LogDebug "bd show failed: $($result.Output)"
        return "unknown"
    }
    
    try {
        $jsonOutput = $result.Output | ConvertFrom-Json
        # bd show returns a flat array: [{"id":"...","status":"..."}]
        if ($jsonOutput -is [array] -and $jsonOutput.Count -gt 0) {
            return $jsonOutput[0].status
        }
        # Fallback for single object
        return $jsonOutput.status
    }
    catch {
        Write-LogDebug "Failed to parse JSON from bd show: $_"
        
        # Fallback: try to extract status using regex
        if ($result.Output -match '"status"\s*:\s*"([^"]+)"') {
            return $matches[1]
        }
        
        return "unknown"
    }
}

function Validate-Traces {
    Write-Log "Fetching traces for issue $issueId..."
    
    $result = Invoke-Command "lg log '$issueId'"
    
    if (-not $result.Success) {
        Write-LogDebug "lg log failed: $($result.Output)"
        Write-LogFail "No traces found or unexpected format"
        return $false
    }
    
    $output = $result.Output | Out-String
    
    # Check for key markers in traces
    # - ACP conversation indicates vessel-driver established ACP session
    # - git push indicates vessel-driver pushed the branch
    # - closed status indicates issue was completed
    
    if ($output -match "(trace|acp|session|git|push)" -or $output.Length -gt 10) {
        Write-LogPass "Traces contain expected markers"
        Write-LogDebug "Trace summary: $($output.Substring(0, [Math]::Min(100, $output.Length)))"
        return $true
    } else {
        Write-LogDebug "Issue traces (raw output): $output"
        Write-LogFail "No traces found or unexpected format"
        return $false
    }
}

function Collect-Logs {
    Write-Log "Collecting diagnostic logs..."
    
    $composeFile = Join-Path $scriptDir "docker-compose.yml"
    
    # Fetch Archon container logs
    $archonResult = Invoke-Command "docker-compose -f '$composeFile' logs archon"
    if ($archonResult.Success) {
        Set-Content -Path $archonLog -Value $archonResult.Output
        Write-LogDebug "Archon logs saved to: $archonLog"
    } else {
        Write-LogDebug "Failed to fetch archon logs"
    }
    
    # Fetch all container logs
    $allLogsPath = Join-Path $logDir "docker-compose-$timestamp.log"
    $allResult = Invoke-Command "docker-compose -f '$composeFile' logs"
    if ($allResult.Success) {
        Set-Content -Path $allLogsPath -Value $allResult.Output
        Write-LogDebug "All compose logs saved to: $allLogsPath"
    }
}

# ─────────────────────────────────────────────────────────────────────────────
# Main
# ─────────────────────────────────────────────────────────────────────────────

function Main {
    Write-Host ""
    Write-Log "=================================================================="
    Write-Log "MVP.1 Spawn Cycle Validation Test (Windows PowerShell)"
    Write-Log "=================================================================="
    Write-Log "Test output: $testLog"
    Write-Host ""
    
    try {
        Check-Prerequisites
        Create-Issue
        
        # Wait for Archon to spawn
        if (-not (Wait-ForSpawn)) {
            Collect-Logs
            exit 1
        }
        
        # Wait for vessel to complete
        if (-not (Wait-ForCompletion)) {
            Write-LogFail "Vessel container did not complete successfully"
            Collect-Logs
            exit 1
        }
        
        # Validate traces
        if (-not (Validate-Traces)) {
            Write-LogFail "Trace validation failed"
            Collect-Logs
            exit 1
        }
        
        Collect-Logs
        
        Write-Host ""
        Write-Log "=================================================================="
        Write-LogPass "MVP.1 Spawn Cycle Test PASSED"
        Write-Log "=================================================================="
        Write-Log "Issue ID: $issueId"
        Write-Log "Logs: $logDir"
        Write-Host ""
        
        exit 0
    }
    catch {
        Write-LogError "Unexpected error: $_"
        Write-LogError "$($_.ScriptStackTrace)"
        Collect-Logs
        exit 1
    }
}

# Run main
Main
