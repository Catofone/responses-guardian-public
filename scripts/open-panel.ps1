$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'guardian-common.ps1')
$settings = Get-GuardianSettings -Root $root -Active
$health = Get-GuardianHealth -Settings $settings
if ($null -eq $health) { throw 'Guardian is not running. Start it before opening the panel.' }
Assert-GuardianProcess -Root $root -Settings $settings -Health $health | Out-Null
if ([version]$health.version -lt [version]'0.4.0') { throw 'The running Guardian has no control panel. Close Codex once and run .\scripts\activate-update.ps1.' }
$panelURL = "http://$($settings.listen)/__responses_guardian/panel"
Start-Process -FilePath $panelURL
Write-Host "Opened $panelURL"
