param(
    [ValidateSet('off', 'on-error', 'always')]
    [string]$Mode
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'guardian-common.ps1')
$settings = Get-GuardianSettings -Root $root -Active
$activeConnections = @(Get-GuardianConnections -Settings $settings -State Established)
$codexProcesses = @(Get-CimInstance Win32_Process | Where-Object {
    ($_.Name -ieq 'codex.exe') -or
    (($_.Name -ieq 'ChatGPT.exe') -and $_.ExecutablePath -and ($_.ExecutablePath -match '(?i)OpenAI[.\\]Codex'))
})
if ($activeConnections.Count -gt 0 -or $codexProcesses.Count -gt 0) {
    throw 'Close Codex completely before replacing its active proxy binary. To change modes on Guardian 0.3.0+, use .\scripts\set-mode.ps1 instead.'
}

if (-not $PSBoundParameters.ContainsKey('Mode')) {
    $health = Get-GuardianHealth -Settings $settings
    if ($null -ne $health) {
        if ($health.mode) { $Mode = [string]$health.mode } else { $Mode = 'on-error' }
    } else {
        $Mode = Read-GuardianSavedMode -Path (Join-Path $root 'runtime\mode.json')
    }
}

# Use a child shell so a stop failure cannot exit this script before checking it.
$shellPath = (Get-Process -Id $PID).Path
& $shellPath -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'stop-ccswitch.ps1')
if ($LASTEXITCODE -ne 0) { throw 'Stopping the previous Guardian failed; update was not applied.' }
& (Join-Path $PSScriptRoot 'start-ccswitch.ps1') -Mode $Mode
Write-Host "Guardian update is active in '$Mode' mode. Start Codex now."
