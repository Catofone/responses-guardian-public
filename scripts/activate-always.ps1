$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'guardian-common.ps1')
$settings = Get-GuardianSettings -Root $root -Active
$health = Get-GuardianHealth -Settings $settings
if ($null -ne $health -and $health.mode_switch_supported) {
    & (Join-Path $PSScriptRoot 'set-mode.ps1') -Mode always
} else {
    & (Join-Path $PSScriptRoot 'activate-update.ps1') -Mode always
}
