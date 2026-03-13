#!/usr/bin/env pwsh
# test-bd-init.ps1 — Clone legion into a temp dir and test bd init --quiet
# Usage: .\test-bd-init.ps1 [-Repo <url>] [-Keep]

param(
    [string]$Repo = "https://github.com/EmmittJ/legion.git",
    [switch]$Keep   # keep the temp dir after the run
)

# Kill orphaned dolt server processes before starting
Write-Host "=== PREFLIGHT: kill orphaned dolt processes ===" -ForegroundColor Cyan
Get-Process -Name "dolt" -ErrorAction SilentlyContinue | ForEach-Object {
    Write-Host "  Killing dolt PID $($_.Id)"
    Stop-Process -Id $_.Id -Force
}

$tmp = "$env:TEMP\legion-bd-test-$(Get-Random)"

Write-Host "`n=== STEP 1: git clone ===" -ForegroundColor Cyan
git clone $Repo $tmp
if ($LASTEXITCODE -ne 0) { Write-Host "CLONE FAILED" -ForegroundColor Red; exit 1 }

Set-Location $tmp
Write-Host "Working dir: $tmp`n"

Write-Host "=== STEP 2: bd init --quiet (empty stdin file to simulate non-TTY) ===" -ForegroundColor Cyan
$nullIn = "$tmp\null-stdin.txt"
"" | Set-Content $nullIn
$proc = Start-Process -FilePath "bd" -ArgumentList "init","--quiet" `
    -WorkingDirectory $tmp `
    -RedirectStandardInput $nullIn `
    -RedirectStandardOutput "$tmp\bd-init.log" `
    -RedirectStandardError "$tmp\bd-init-err.log" `
    -PassThru -NoNewWindow

# Wait for dolt-server.pid to appear (server is up) or process to exit
$timeout = 60
$elapsed = 0
while (-not (Test-Path "$tmp\.beads\dolt-server.pid") -and -not $proc.HasExited -and $elapsed -lt $timeout) {
    Start-Sleep 1; $elapsed++
}
$proc.WaitForExit(10000) | Out-Null
$initExit = $proc.ExitCode
Write-Host "Exit code: $initExit  (waited ${elapsed}s)"
Write-Host "--- bd init stdout ---"
Get-Content "$tmp\bd-init.log" -ErrorAction SilentlyContinue
Write-Host "--- bd init stderr ---"
Get-Content "$tmp\bd-init-err.log" -ErrorAction SilentlyContinue

Write-Host "`n=== STEP 3: bd dolt remote add + pull ===" -ForegroundColor Cyan
bd dolt remote add origin "git+https://github.com/EmmittJ/legion.git" 2>&1
bd dolt pull 2>&1
$pullExit = $LASTEXITCODE
Write-Host "pull exit: $pullExit"

Write-Host "`n=== STEP 5: bd dolt remote list ===" -ForegroundColor Cyan
bd dolt remote list 2>&1

Write-Host "`n=== STEP 6: bd ready --json ===" -ForegroundColor Cyan
bd ready --json
$readyExit = $LASTEXITCODE

Write-Host "`n=== STEP 7: issue count ===" -ForegroundColor Cyan
$issues = bd list --status open --json 2>&1 | ConvertFrom-Json
Write-Host "Total open issues: $($issues.Count)"

Write-Host "`n=== SUMMARY ===" -ForegroundColor Cyan
Write-Host "bd init exit:  $initExit"
Write-Host "bd pull exit:  $pullExit"
Write-Host "bd ready exit: $readyExit"

if (-not $Keep) {
    Set-Location D:\GitHub\EmmittJ\legion
    Remove-Item -Recurse -Force $tmp
    Write-Host "Cleaned up $tmp"
} else {
    Write-Host "Kept temp dir: $tmp"
}
