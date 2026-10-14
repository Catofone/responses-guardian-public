$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot

try {
    . (Join-Path $PSScriptRoot 'guardian-common.ps1')
    $settings = Get-GuardianSettings -Root $root -Active
    $health = Get-GuardianHealth -Settings $settings
    if ($null -eq $health) {
        # DefaultMode applies only to a new start without a saved preference.
        # If another launch won the race, its running mode is left intact.
        & (Join-Path $PSScriptRoot 'start-ccswitch.ps1') -DefaultMode off
    }
    & (Join-Path $PSScriptRoot 'open-panel.ps1')
} catch {
    [Console]::Error.WriteLine('Responses Guardian launcher failed:')
    [Console]::Error.WriteLine($_.Exception.Message)
    exit 1
}
