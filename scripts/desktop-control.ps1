param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('connect', 'status', 'stop', 'save', 'mode')]
    [string]$Action,
    [Parameter(Mandatory = $true)]
    [string]$ResultFile,
    [string]$SettingsFile,
    [ValidateSet('off', 'on-error', 'always')]
    [string]$Mode
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$resultPathValid = $false
$startedAt = Get-Date
$attemptedStart = $false

function Write-DesktopResult {
    param($Result)
    $utf8 = New-Object System.Text.UTF8Encoding($false)
    $temporary = $ResultFile + '.tmp'
    try {
        [System.IO.File]::WriteAllText($temporary, ($Result | ConvertTo-Json -Depth 6 -Compress), $utf8)
        Move-Item -LiteralPath $temporary -Destination $ResultFile -Force
    } finally {
        if (Test-Path -LiteralPath $temporary) { Remove-Item -LiteralPath $temporary -Force }
    }
}

try {
    $runtime = [System.IO.Path]::GetFullPath((Join-Path $root 'runtime'))
    $ResultFile = [System.IO.Path]::GetFullPath($ResultFile)
    if ((Split-Path -Parent $ResultFile) -ne $runtime -or (Split-Path -Leaf $ResultFile) -notmatch '^desktop-operation-[a-f0-9]{32}\.json$') {
        throw 'Invalid desktop result path.'
    }
    $resultPathValid = $true
    New-Item -ItemType Directory -Path $runtime -Force | Out-Null
    . (Join-Path $PSScriptRoot 'guardian-common.ps1')

    if ($Action -eq 'mode') {
        if (-not $Mode) { throw 'Choose off, on-error or always.' }
        Write-GuardianMode -Path (Join-Path $runtime 'mode.json') -Mode $Mode
        Write-DesktopResult @{ ok = $true; running = $false; mode = $Mode }
        return
    }

    if ($Action -eq 'save') {
        $SettingsFile = [System.IO.Path]::GetFullPath($SettingsFile)
        if ((Split-Path -Parent $SettingsFile) -ne $runtime -or (Split-Path -Leaf $SettingsFile) -notmatch '^desktop-input-[a-f0-9]{32}\.json$') {
            throw 'Invalid desktop settings input path.'
        }
        $settingsTarget = Join-Path $runtime 'settings.json'
        $oldSettings = if (Test-Path -LiteralPath $settingsTarget) { [System.IO.File]::ReadAllText($settingsTarget) } else { $null }
        $validationExe = Join-Path $root 'responses-guardian.exe'
        $pending = Join-Path $root 'responses-guardian.next.exe'
        if (Test-Path -LiteralPath $pending) {
            $checksum = Join-Path $root 'responses-guardian.next.sha256'
            if (-not (Test-Path -LiteralPath $checksum)) { throw 'Missing checksum for the staged validator.' }
            $expected = (Get-Content -Raw -LiteralPath $checksum).Trim()
            if ($expected -notmatch '^[0-9a-fA-F]{64}$' -or (Get-GuardianFileHash -Path $pending) -ne $expected) {
                throw 'The staged validator checksum does not match.'
            }
            $validationExe = $pending
        }
        if ([version](& $validationExe --version) -lt [version]'0.4.2') { throw 'Offline settings validation requires Guardian 0.4.2 or its staged update.' }
        $validationLog = $ResultFile + '.validation.log'
        $validationOutput = $ResultFile + '.validation.stdout'
        try {
            $instancePath = Join-Path $runtime 'instance.json'
            $validationArguments = '--check-settings --settings-file "' + $SettingsFile + '" --instance-file "' + $instancePath + '"'
            $validation = Start-Process -FilePath $validationExe -ArgumentList $validationArguments -WindowStyle Hidden -Wait -PassThru -RedirectStandardError $validationLog -RedirectStandardOutput $validationOutput
            if ($validation.ExitCode -ne 0) {
                throw ('Settings were not saved: ' + [System.IO.File]::ReadAllText($validationLog))
            }
        } finally {
            foreach ($path in @($validationLog, $validationOutput)) { if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path -Force } }
        }
        $currentSettings = if (Test-Path -LiteralPath $settingsTarget) { [System.IO.File]::ReadAllText($settingsTarget) } else { $null }
        if ($currentSettings -cne $oldSettings) { throw 'Settings changed during validation. Reconnect before saving again.' }
        $candidate = Get-Content -Raw -LiteralPath $SettingsFile -Encoding UTF8 | ConvertFrom-Json
        Write-GuardianSettings -Root $root -Settings $candidate
        Write-DesktopResult @{ ok = $true; running = $false; message = 'Startup settings saved. Connect or restart the proxy to apply them.' }
        return
    }

    if ($Action -eq 'stop') {
        # stop-ccswitch.ps1 may exit with code 2. A child shell lets this
        # controller still return an error result to the desktop window.
        & (Join-Path $PSHOME 'powershell.exe') -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'stop-ccswitch.ps1')
        if ($LASTEXITCODE -eq 2) { throw 'Close Codex completely before stopping its proxy.' }
        if ($LASTEXITCODE -ne 0) { throw 'Proxy shutdown or Codex routing restoration failed. Inspect the configuration and log.' }
    }

    $settings = Get-GuardianSettings -Root $root -Active
    $health = Get-GuardianHealth -Settings $settings
    if ($Action -eq 'connect' -and $null -eq $health) {
        $attemptedStart = $true
        & (Join-Path $PSScriptRoot 'start-ccswitch.ps1') -DefaultMode off
        $settings = Get-GuardianSettings -Root $root -Active
        $health = Get-GuardianHealth -Settings $settings
        if ($null -eq $health) { throw 'Guardian started but did not respond to the health check.' }
    }

    $panelURL = ''
    if ($null -ne $health) {
        Assert-GuardianProcess -Root $root -Settings $settings -Health $health | Out-Null
        if ([version]$health.version -lt [version]'0.4.0') {
            throw 'This proxy has no control panel. Close Codex and activate the staged update first.'
        }
        $panelURL = (Get-GuardianEndpoint -Settings $settings).AbsoluteUri.TrimEnd('/') + '/__responses_guardian/panel'
    }
    Write-DesktopResult @{ ok = $true; running = ($null -ne $health); panel_url = $panelURL; health = $health }
} catch {
    $message = $_.Exception.Message
    if ($attemptedStart) {
        $logPath = Join-Path $root 'runtime\responses-guardian.log'
        if ((Test-Path -LiteralPath $logPath) -and (Get-Item -LiteralPath $logPath).LastWriteTime -ge $startedAt) {
            $detail = (Get-Content -LiteralPath $logPath -Tail 6 -Encoding UTF8) -join "`n"
            if ($detail) { $message += "`n" + $detail }
        }
    }
    if ($resultPathValid) {
        try { Write-DesktopResult @{ ok = $false; error = $message } } catch { [Console]::Error.WriteLine($_.Exception.Message) }
    }
    exit 1
}
