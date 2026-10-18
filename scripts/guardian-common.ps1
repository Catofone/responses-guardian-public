function Get-GuardianSettings {
    param([string]$Root, [switch]$Active, [switch]$IncludeStopped)
    $settings = [ordered]@{
        listen = '127.0.0.1:15722'; upstream = 'http://127.0.0.1:15721'; provider = 'custom'
        codex_config = (Join-Path $env:USERPROFILE '.codex\config.toml')
        request_max_retries = 20; stream_max_retries = 20
    }
    $savedPath = Join-Path $Root 'runtime\settings.json'
    if (Test-Path -LiteralPath $savedPath) {
        $saved = Get-Content -Raw -Encoding UTF8 -LiteralPath $savedPath | ConvertFrom-Json
        foreach ($name in @($settings.Keys)) {
            if ($null -ne $saved.PSObject.Properties[$name]) { $settings[$name] = $saved.$name }
        }
    } elseif (Test-Path -LiteralPath $settings.codex_config) {
        # Initial migration: preserve the current user's retry policy rather
        # than replacing existing values with the panel's example defaults.
        $inProvider = $false
        $lines = Get-Content -Encoding UTF8 -LiteralPath $settings.codex_config
        foreach ($line in $lines) {
            if ($line.Trim().StartsWith('[')) { break }
            if ($line -match '^\s*model_provider\s*=\s*"([A-Za-z0-9_-]{1,64})"') { $settings.provider = $Matches[1] }
        }
        foreach ($line in $lines) {
            $trimmed = $line.Trim()
            if ($trimmed.StartsWith('[')) {
                $inProvider = ($trimmed -eq ('[model_providers.' + $settings.provider + ']') -or $trimmed -eq ('[model_providers."' + $settings.provider + '"]'))
            }
            if ($inProvider -and $trimmed -match '^(request_max_retries|stream_max_retries)\s*=\s*(\d+)\s*(#.*)?$') {
                $number = [int]$Matches[2]
                if ($number -ge 0 -and $number -le 100) { $settings[$Matches[1]] = $number }
            }
        }
    }
    $instancePath = Join-Path $Root 'runtime\instance.json'
    if ($Active -and (Test-Path -LiteralPath $instancePath)) {
        $instance = Get-Content -Raw -Encoding UTF8 -LiteralPath $instancePath | ConvertFrom-Json
        $process = Get-Process -Id ([int]$instance.pid) -ErrorAction SilentlyContinue
        $expected = [System.IO.Path]::GetFullPath((Join-Path $Root 'responses-guardian.exe'))
        if ($IncludeStopped -or ($process -and $process.Path -and [System.IO.Path]::GetFullPath($process.Path) -eq $expected)) {
            foreach ($name in @($settings.Keys)) {
                if ($null -ne $instance.settings.PSObject.Properties[$name]) { $settings[$name] = $instance.settings.$name }
            }
        }
    }
    $listen = [uri]('http://' + $settings.listen)
    $upstream = [uri]$settings.upstream
    if (-not $listen.IsLoopback -or -not $upstream.IsLoopback -or $upstream.Scheme -notin @('http','https') -or $upstream.UserInfo) {
        throw 'Guardian settings must use local loopback addresses.'
    }
    return [pscustomobject]$settings
}

function Get-GuardianPort {
    param($Settings)
    return ([uri]('http://' + $Settings.listen)).Port
}

function Get-GuardianEndpoint {
    param($Settings, [ValidateRange(0, 65535)][int]$Port = 0)
    $listen = if ($Settings) { [string]$Settings.listen } else { '127.0.0.1:15722' }
    $endpoint = New-Object System.UriBuilder ('http://' + $listen)
    if ($Port -gt 0) { $endpoint.Port = $Port }
    if (-not $endpoint.Uri.IsLoopback -or $endpoint.Uri.UserInfo -or $endpoint.Uri.AbsolutePath -ne '/' -or $endpoint.Uri.Query -or $endpoint.Uri.Fragment) {
        throw 'Guardian listener must be a local HTTP endpoint.'
    }
    return $endpoint.Uri
}

function Get-GuardianConnections {
    param($Settings, [int]$Port = 0, [ValidateSet('Listen', 'Established')][string]$State = 'Listen')
    $endpoint = Get-GuardianEndpoint -Settings $Settings -Port $Port
    $addresses = @([System.Net.Dns]::GetHostAddresses($endpoint.DnsSafeHost) | ForEach-Object { $_.ToString() })
    Get-NetTCPConnection -State $State -LocalPort $endpoint.Port -ErrorAction SilentlyContinue |
        Where-Object { $addresses -contains ([System.Net.IPAddress]::Parse($_.LocalAddress).ToString()) }
}

function Write-GuardianSettings {
    param([string]$Root, $Settings)
    $directory = Join-Path $Root 'runtime'
    New-Item -ItemType Directory -Force -Path $directory | Out-Null
    $target = Join-Path $directory 'settings.json'
    $temporary = Join-Path $directory ('.settings-' + [guid]::NewGuid().ToString('N') + '.tmp')
    $utf8 = New-Object System.Text.UTF8Encoding($false)
    try {
        [System.IO.File]::WriteAllText($temporary, (($Settings | ConvertTo-Json -Depth 4) + "`n"), $utf8)
        Move-Item -LiteralPath $temporary -Destination $target -Force
    } finally {
        if (Test-Path -LiteralPath $temporary) { Remove-Item -LiteralPath $temporary -Force }
    }
}

function Get-GuardianHealth {
    param([int]$Port = 0, $Settings)
    $endpoint = Get-GuardianEndpoint -Settings $Settings -Port $Port
    try {
        return Invoke-RestMethod -Uri ($endpoint.AbsoluteUri.TrimEnd('/') + '/__responses_guardian/health') -TimeoutSec 2
    } catch {
        return $null
    }
}

function Assert-GuardianProcess {
    param([string]$Root, [int]$Port = 0, $Health, $Settings)
    $endpoint = Get-GuardianEndpoint -Settings $Settings -Port $Port
    $Port = $endpoint.Port
    $listeners = @(Get-GuardianConnections -Settings $Settings -Port $Port)
    $processIds = @($listeners | Select-Object -ExpandProperty OwningProcess -Unique)
    if ($processIds.Count -ne 1) {
        throw "Expected exactly one Guardian process on port $Port."
    }
    $process = Get-Process -Id $processIds[0] -ErrorAction Stop
    $expectedPath = [System.IO.Path]::GetFullPath((Join-Path $Root 'responses-guardian.exe'))
    if (-not $process.Path -or [System.IO.Path]::GetFullPath($process.Path) -ne $expectedPath) {
        throw "Port $Port belongs to another program."
    }
    if ($Health.pid -and [int]$Health.pid -ne $process.Id) {
        throw 'Guardian restarted during the check. Retry the command.'
    }
    return $process
}

function Read-GuardianSavedMode {
    param([string]$Path, [ValidateSet('off', 'on-error', 'always')][string]$DefaultMode = 'on-error')
    if (-not (Test-Path -LiteralPath $Path)) {
        return $DefaultMode
    }
    $preference = Get-Content -Raw -LiteralPath $Path | ConvertFrom-Json
    if ($preference.mode -notin @('off', 'on-error', 'always')) {
        throw 'The saved mode is invalid. Specify -Mode off, on-error or always to replace it.'
    }
    return [string]$preference.mode
}

function Write-GuardianMode {
    param([string]$Path, [ValidateSet('off', 'on-error', 'always')][string]$Mode)
    $directory = Split-Path -Parent $Path
    New-Item -ItemType Directory -Force -Path $directory | Out-Null
    $temporary = Join-Path $directory ('.mode-' + [guid]::NewGuid().ToString('N') + '.tmp')
    $utf8 = New-Object System.Text.UTF8Encoding($false)
    try {
        [System.IO.File]::WriteAllText($temporary, ('{"mode":"' + $Mode + '"}' + "`n"), $utf8)
        for ($attempt = 0; $attempt -lt 5; $attempt++) {
            try {
                Move-Item -LiteralPath $temporary -Destination $Path -Force
                return
            } catch {
                if ($attempt -eq 4) { throw }
                Start-Sleep -Milliseconds 50
            }
        }
    } finally {
        if (Test-Path -LiteralPath $temporary) {
            Remove-Item -LiteralPath $temporary -Force
        }
    }
}

function Set-GuardianLiveMode {
    param([string]$Root, [int]$Port = 0, [string]$Mode, $Health, $Settings)
    $process = Assert-GuardianProcess -Root $Root -Port $Port -Health $Health -Settings $Settings
    $modePath = [System.IO.Path]::GetFullPath((Join-Path $Root 'runtime\mode.json'))
    if (-not $Health.mode_switch_supported) {
        throw "Guardian $($Health.version) does not support live switching. Close Codex once and run .\scripts\activate-update.ps1 -Mode $Mode to install the staged update."
    }
    if (-not $Health.mode_file -or [System.IO.Path]::GetFullPath([string]$Health.mode_file) -ne $modePath) {
        throw 'The running Guardian uses a different mode file. Use the scripts in its own directory.'
    }
    Write-GuardianMode -Path $modePath -Mode $Mode
    $timer = [System.Diagnostics.Stopwatch]::StartNew()
    do {
        $confirmed = Get-GuardianHealth -Port $Port -Settings $Settings
        if ($null -ne $confirmed) {
            if ([int]$confirmed.pid -ne $process.Id) {
                throw 'Guardian restarted during the mode switch. Check .\scripts\status.ps1.'
            }
            if ($confirmed.mode -eq $Mode) {
                return $confirmed
            }
        }
        Start-Sleep -Milliseconds 150
    } while ($timer.Elapsed.TotalSeconds -lt 6)
    throw 'The preference was saved, but Guardian did not confirm the new mode. Check .\scripts\status.ps1; no restart was attempted.'
}

function Get-GuardianFileHash {
    param([string]$Path)
    $stream = [System.IO.File]::OpenRead($Path)
    $algorithm = [System.Security.Cryptography.SHA256]::Create()
    try { return [System.BitConverter]::ToString($algorithm.ComputeHash($stream)).Replace('-', '') }
    finally { $algorithm.Dispose(); $stream.Dispose() }
}

function Update-GuardianChecksums {
    param([string]$Root)
    $lines = @()
    foreach ($name in @('Guardian.exe', 'responses-guardian.exe', 'responses-guardian.next.exe', 'responses-guardian.previous.exe')) {
        $path = Join-Path $Root $name
        if (Test-Path -LiteralPath $path) {
            $lines += (Get-GuardianFileHash -Path $path) + '  ' + $name
        }
    }
    Set-Content -LiteralPath (Join-Path $Root 'SHA256SUMS.txt') -Value $lines -Encoding ascii
}

function Install-GuardianPendingUpdate {
    param([string]$Root)
    $pending = Join-Path $Root 'responses-guardian.next.exe'
    if (-not (Test-Path -LiteralPath $pending)) { return }
    $hashFile = Join-Path $Root 'responses-guardian.next.sha256'
    if (-not (Test-Path -LiteralPath $hashFile)) {
        throw 'Missing checksum for the staged Guardian update.'
    }
    $expectedHash = (Get-Content -Raw -LiteralPath $hashFile).Trim()
    if ($expectedHash -notmatch '^[0-9a-fA-F]{64}$' -or (Get-GuardianFileHash -Path $pending) -ne $expectedHash) {
        throw 'The staged Guardian update checksum does not match.'
    }
    $target = Join-Path $Root 'responses-guardian.exe'
    $backup = Join-Path $Root 'responses-guardian.previous.exe'
    if (Test-Path -LiteralPath $target) {
        [System.IO.File]::Copy($target, $backup, $true)
    }
    Move-Item -LiteralPath $pending -Destination $target -Force
    Remove-Item -LiteralPath $hashFile -Force
    Update-GuardianChecksums -Root $Root
    Write-Host 'Applied staged Guardian update. The previous binary is kept as responses-guardian.previous.exe.'
}
