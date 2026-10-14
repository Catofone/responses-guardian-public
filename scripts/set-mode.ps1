param(
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateSet('off', 'on-error', 'always')]
    [string]$Mode,
    [ValidateRange(0, 65535)]
    [int]$Port = 0
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'guardian-common.ps1')
$settings = Get-GuardianSettings -Root $root -Active
$health = Get-GuardianHealth -Settings $settings -Port $Port
if ($null -eq $health) {
    throw 'Guardian is not reachable. Run .\scripts\start-ccswitch.ps1 first.'
}
$confirmed = Set-GuardianLiveMode -Root $root -Settings $settings -Port $Port -Mode $Mode -Health $health
Write-Host "Guardian mode: $($confirmed.mode) (version $($confirmed.version), PID $($confirmed.pid))."
Write-Host 'New requests use this mode. Existing requests and streams continue without interruption.'
