param(
    [switch]$Force
)

$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'guardian-common.ps1')
$exe = Join-Path $root 'responses-guardian.exe'
$pidFile = Join-Path $root 'runtime\responses-guardian.pid'
$settings = Get-GuardianSettings -Root $root -Active -IncludeStopped

$activeConnections = @(Get-GuardianConnections -Settings $settings -State Established)
$codexProcesses = @(Get-CimInstance Win32_Process | Where-Object {
    ($_.Name -ieq 'codex.exe') -or
    (($_.Name -ieq 'ChatGPT.exe') -and $_.ExecutablePath -and ($_.ExecutablePath -match '(?i)OpenAI[.\\]Codex'))
})
if (($activeConnections.Count -gt 0 -or $codexProcesses.Count -gt 0) -and -not $Force) {
    Write-Warning 'Codex is still running or Guardian has active Codex connections. No process or configuration was changed.'
    Write-Warning 'Close Codex completely, then run: .\scripts\stop-ccswitch.ps1'
    exit 2
}

$expectedPath = [System.IO.Path]::GetFullPath($exe)
$candidateIds = @()
if (Test-Path -LiteralPath $pidFile) {
    $rawPid = (Get-Content -Raw -LiteralPath $pidFile).Trim()
    if ($rawPid -match '^\d+$') {
        $candidateIds += [int]$rawPid
    }
}
$candidateIds += @(Get-CimInstance Win32_Process | Where-Object {
    $_.ExecutablePath -and ([System.IO.Path]::GetFullPath($_.ExecutablePath) -eq $expectedPath)
} | Select-Object -ExpandProperty ProcessId)

foreach ($pidValue in @($candidateIds | Sort-Object -Unique)) {
    $process = Get-Process -Id $pidValue -ErrorAction SilentlyContinue
    if ($null -eq $process) {
        continue
    }
    $actualPath = $process.Path
    if (-not $actualPath -or ([System.IO.Path]::GetFullPath($actualPath) -ne $expectedPath)) {
        throw "PID $pidValue belongs to a different executable; refusing to stop it."
    }
    Stop-Process -Id $pidValue -Force
    $process.WaitForExit(5000) | Out-Null
}

if (Test-Path -LiteralPath $pidFile) {
    Remove-Item -LiteralPath $pidFile -Force
}

$guardianVersion = & $exe --version
if ([version]$guardianVersion -ge [version]'0.4.0') {
    Push-Location $root
    try { & $exe --restore-config --settings-file runtime/settings.json --instance-file runtime/instance.json } finally { Pop-Location }
} else { & $exe --restore-config }
if ($LASTEXITCODE -ne 0) {
    throw 'Guardian was stopped, but Codex routing could not be restored. Inspect config.toml before starting Codex.'
}
Write-Host 'Responses Guardian stopped and Codex routing was restored to CCSwitch.'
