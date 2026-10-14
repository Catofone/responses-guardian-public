param(
    [ValidateSet('off', 'on-error', 'always')]
    [string]$Mode,
    [ValidateSet('off', 'on-error', 'always')]
    [string]$DefaultMode = 'on-error'
)

$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'guardian-common.ps1')
$exe = Join-Path $root 'responses-guardian.exe'
$pendingExe = Join-Path $root 'responses-guardian.next.exe'
$runtime = Join-Path $root 'runtime'
$modePath = Join-Path $runtime 'mode.json'
$pidFile = Join-Path $runtime 'responses-guardian.pid'
$stdoutLog = Join-Path $runtime 'responses-guardian.stdout.log'
$stderrLog = Join-Path $runtime 'responses-guardian.log'
$explicitMode = $PSBoundParameters.ContainsKey('Mode')
$activeSettings = Get-GuardianSettings -Root $root -Active
$existing = Get-GuardianHealth -Settings $activeSettings

if ($null -ne $existing) {
    $running = Assert-GuardianProcess -Root $root -Settings $activeSettings -Health $existing
    $runningMode = if ([string]::IsNullOrWhiteSpace([string]$existing.mode)) { 'on-error' } else { [string]$existing.mode }
    if ($explicitMode) {
        if ($existing.mode_switch_supported) {
            $existing = Set-GuardianLiveMode -Root $root -Settings $activeSettings -Mode $Mode -Health $existing
            $runningMode = [string]$existing.mode
        } elseif ($runningMode -ne $Mode) {
            throw "Guardian $($existing.version) cannot switch modes live. Close Codex once and run .\scripts\activate-update.ps1 -Mode $Mode."
        }
    }
    New-Item -ItemType Directory -Force -Path $runtime | Out-Null
    Set-Content -LiteralPath $pidFile -Value $running.Id -Encoding ascii
    if (Test-Path -LiteralPath $pendingExe) {
        Write-Host 'A newer binary is staged; the current process remains running.'
    }
    Write-Host "Responses Guardian is running (version $($existing.version), mode $runningMode, PID $($running.Id))."
    return
}

$staleProcesses = @(Get-CimInstance Win32_Process | Where-Object {
    $_.ExecutablePath -and ([System.IO.Path]::GetFullPath($_.ExecutablePath) -eq [System.IO.Path]::GetFullPath($exe))
})
if ($staleProcesses.Count -gt 0) {
    throw 'A Guardian process exists but is not responding. Check its log before trying to update it.'
}
$settings = Get-GuardianSettings -Root $root
$port = Get-GuardianPort -Settings $settings
$healthUrl = "http://$($settings.listen)/__responses_guardian/health"
$occupiedPort = @(Get-GuardianConnections -Settings $settings)
if ($occupiedPort.Count -gt 0) { throw "Port $port is already in use." }

try {
    $ccswitch = Invoke-RestMethod -Uri ($settings.upstream.TrimEnd('/') + '/health') -TimeoutSec 3
    if ($ccswitch.status -ne 'healthy') { throw 'Unhealthy CCSwitch' }
} catch {
    throw "Start CCSwitch and enable its local proxy at $($settings.upstream) first."
}

Install-GuardianPendingUpdate -Root $root
if (-not (Test-Path -LiteralPath $exe)) { throw "Missing executable: $exe" }
$guardianVersion = [version](& $exe --version)
if (-not (Test-Path -LiteralPath (Join-Path $runtime 'settings.json'))) { Write-GuardianSettings -Root $root -Settings $settings }
if (-not $explicitMode) { $Mode = Read-GuardianSavedMode -Path $modePath -DefaultMode $DefaultMode }
Write-GuardianMode -Path $modePath -Mode $Mode

# WorkingDirectory avoids quoting user paths in the native argument string.
$process = Start-Process `
    -FilePath $exe `
    -WorkingDirectory $root `
    -ArgumentList "--manage-config --mode $Mode --mode-file runtime/mode.json --settings-file runtime/settings.json --instance-file runtime/instance.json" `
    -WindowStyle Hidden `
    -RedirectStandardOutput $stdoutLog `
    -RedirectStandardError $stderrLog `
    -PassThru

Set-Content -LiteralPath $pidFile -Value $process.Id -Encoding ascii

$ready = $false
$timer = [System.Diagnostics.Stopwatch]::StartNew()
do {
    $health = Get-GuardianHealth -Settings $settings
    if ($null -ne $health -and [int]$health.pid -eq $process.Id -and $health.mode -eq $Mode -and $health.mode_switch_supported) {
        $ready = $true
        break
    }
    $process.Refresh()
    if ($process.HasExited) { break }
    Start-Sleep -Milliseconds 150
} while ($timer.Elapsed.TotalSeconds -lt 8)

if (-not $ready) {
    $process.Refresh()
    if (-not $process.HasExited) {
        throw "Guardian has started but its mode is not confirmed. The process was left running. See $stderrLog"
    }
    if ((Test-Path -LiteralPath $pidFile) -and (Get-Content -Raw -LiteralPath $pidFile).Trim() -eq [string]$process.Id) {
        Remove-Item -LiteralPath $pidFile -Force
    }
    if ($guardianVersion -lt [version]'0.4.1') {
        # Legacy executables do not roll back failed startups themselves. Use
        # this attempt's addresses, never a stale instance.json.
        & $exe --restore-config --listen $settings.listen --upstream $settings.upstream --provider $settings.provider --codex-config $settings.codex_config
    }
    throw "Guardian exited before startup completed. See $stderrLog for the error and routing rollback result."
}

Write-Host "Responses Guardian is running (version $($health.version), mode $($health.mode), PID $($health.pid)): $healthUrl"
Write-Host "Control panel: http://$($settings.listen)/__responses_guardian/panel"
Write-Host 'Restart Codex once if its loaded base_url still points directly at CCSwitch. Mode switches need no restart.'
