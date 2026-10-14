$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'guardian-common.ps1')
$settings = Get-GuardianSettings -Root $root -Active
$guardianUrl = (Get-GuardianEndpoint -Settings $settings).AbsoluteUri.TrimEnd('/') + '/__responses_guardian/health'
$ccswitchUrl = $settings.upstream.TrimEnd('/') + '/health'

try {
    $guardian = Invoke-RestMethod -Uri $guardianUrl -TimeoutSec 2
    Write-Host 'Responses Guardian:'
    $guardian | Format-List
    if (Test-Path -LiteralPath (Join-Path $root 'responses-guardian.next.exe')) {
        $pendingVersion = & (Join-Path $root 'responses-guardian.next.exe') --version
        Write-Host "Staged update: $pendingVersion"
    }
} catch {
    Write-Host 'Responses Guardian: not reachable'
}

try {
    $ccswitch = Invoke-RestMethod -Uri $ccswitchUrl -TimeoutSec 2
    Write-Host 'CCSwitch:'
    $ccswitch | Format-List
} catch {
    Write-Host 'CCSwitch: not reachable'
}
