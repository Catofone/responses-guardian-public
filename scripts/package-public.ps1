param(
    [string]$OutputDirectory = (Join-Path (Split-Path -Parent $PSScriptRoot) 'artifacts\public'),
    [switch]$ValidateOnly
)

$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot)).TrimEnd('\')
$manifest = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $PSScriptRoot 'public-files.json') | ConvertFrom-Json
if ($manifest.desktopVersion -notmatch '^\d+\.\d+\.\d+$' -or $manifest.proxyVersion -notmatch '^\d+\.\d+\.\d+$') {
    throw 'Invalid public version manifest.'
}
$files = @($manifest.files)
if ($files.Count -eq 0) { throw 'The public file allowlist is empty.' }
$seen = @{}
foreach ($relative in $files) {
    if ($relative -isnot [string]) { throw 'Public file paths must be strings.' }
    if ([IO.Path]::IsPathRooted($relative) -or $relative -match '(^|[/\\])\.{1,2}([/\\]|$)') {
        throw ('Invalid public path: ' + $relative)
    }
    $normalized = $relative.Replace('\','/')
    if ($seen.ContainsKey($normalized)) { throw ('Duplicate public path: ' + $relative) }
    if ($normalized -match '(^|/)(\.git|\.codex|\.agents|\.claude|\.cursor|runtime|backups|skins|private[^/]*|local-assets|artifacts)(/|$)' -or
        $normalized -match '(?i)(^|/)(AGENTS|CLAUDE|GEMINI)([._-][^/]*)?\.md$|(^|/)(SKILL|copilot-instructions)\.md$|\.prompt\.md$|^docs/(GIT|GITHUB|.*REVIEW[^/]*|PRIVATE[^/]*)\.zh-CN\.md$') {
        throw ('Private content is not permitted in the public allowlist: ' + $relative)
    }
    $source = [IO.Path]::GetFullPath((Join-Path $root $relative))
    if (-not $source.StartsWith($root + '\',[StringComparison]::OrdinalIgnoreCase) -or -not (Test-Path -LiteralPath $source -PathType Leaf)) {
        throw ('Missing or invalid public source: ' + $relative)
    }
    $item = Get-Item -LiteralPath $source -Force
    while ($null -ne $item -and $item.FullName -ne $root) {
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw ('Linked public source is not allowed: ' + $relative) }
        $item = if ($item -is [IO.DirectoryInfo]) { $item.Parent } else { $item.Directory }
    }
    $seen[$normalized] = $true
}

# Validate editorial content and links before writing any release files.
foreach ($relative in $files) {
    if ([IO.Path]::GetExtension($relative) -notin @('.md','.txt')) { continue }
    $source = Join-Path $root $relative
    $text = [IO.File]::ReadAllText($source)
    if ($text -match '(?i)codex://threads/|\b[A-Z]:[\\/]|\b(baseline|layout|backup)-\d{4}-\d{2}-\d{2}\b' -or
        $text -match '(?m)^\s*>\s*(Use case:|Make a precise correction edit|Edit the supplied character reference)') {
        throw ('Personal notes or production prompts found in public documentation: ' + $relative)
    }
    if ([IO.Path]::GetExtension($relative) -ne '.md') { continue }
    foreach ($match in [regex]::Matches($text,'\[[^\]]*\]\(([^)]+)\)')) {
        $target = $match.Groups[1].Value
        if ($target -match '^(https?://|mailto:|#)') { continue }
        $target = $target.Split('#')[0]
        $absolute = [IO.Path]::GetFullPath((Join-Path (Split-Path -Parent $source) $target))
        if (-not $absolute.StartsWith($root + '\',[StringComparison]::OrdinalIgnoreCase)) { throw ('External local documentation link: ' + $target) }
        $targetRelative = $absolute.Substring($root.Length + 1).Replace('\','/')
        if (-not $seen.ContainsKey($targetRelative)) { throw ('Documentation links outside the public allowlist: ' + $target) }
    }
}
$programText = [IO.File]::ReadAllText((Join-Path $root 'desktop\Program.cs'))
$assemblyVersion = [regex]::Match($programText,'AssemblyVersion\s*\(\s*"([^"]+)"\s*\)').Groups[1].Value
$proxyText = [IO.File]::ReadAllText((Join-Path $root 'cmd\responses-guardian\main.go'))
$proxyVersion = [regex]::Match($proxyText,'const\s+version\s*=\s*"([^"]+)"').Groups[1].Value
if ($assemblyVersion -ne ($manifest.desktopVersion + '.0') -or $proxyVersion -ne $manifest.proxyVersion) { throw 'Source and public package versions disagree.' }
foreach ($document in @('README.md','docs\DESKTOP.zh-CN.md')) {
    if (-not [IO.File]::ReadAllText((Join-Path $root $document)).Contains($manifest.desktopVersion)) { throw ('Desktop version is missing from ' + $document) }
}
if ($ValidateOnly) {
    [pscustomobject]@{ desktopVersion=$manifest.desktopVersion; proxyVersion=$manifest.proxyVersion; files=$files } | ConvertTo-Json -Depth 4
    return
}

$output = [IO.Path]::GetFullPath($OutputDirectory)
$packageName = 'responses-guardian-desktop-' + $manifest.desktopVersion
$release = Join-Path $output ($packageName + '-windows-x64')
$windowsZip = $release + '.zip'
$sourceZip = Join-Path $output ($packageName + '-source.zip')
foreach ($path in @($release,$windowsZip,$sourceZip)) { if (Test-Path -LiteralPath $path) { throw ('Release output already exists: ' + $path) } }
New-Item -ItemType Directory -Path $release -Force | Out-Null
foreach ($relative in $files) {
    $target = Join-Path $release $relative
    New-Item -ItemType Directory -Path (Split-Path -Parent $target) -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $root $relative) -Destination $target
}

# Build only from the reviewed export; installed executables and local skins are not inputs.
& (Join-Path $release 'scripts\build-desktop.ps1') -OutputPath (Join-Path $release 'Guardian.exe')
if ($LASTEXITCODE -ne 0) { throw 'Public desktop build failed.' }
Push-Location $release
try {
    go build -trimpath -buildvcs=false -ldflags '-s -w' -o responses-guardian.exe ./cmd/responses-guardian
    if ($LASTEXITCODE -ne 0) { throw 'Public proxy build failed.' }
} finally { Pop-Location }
$assembly = [Reflection.Assembly]::Load([IO.File]::ReadAllBytes((Join-Path $release 'Guardian.exe')))
$resources = @($assembly.GetManifestResourceNames())
if ($resources.Count -ne 1 -or $resources[0] -ne 'GuardianIcon') { throw 'Unexpected embedded desktop resource.' }
if ($assembly.GetName().Version.ToString() -ne $assemblyVersion) { throw 'Built desktop version mismatch.' }
$resource = $assembly.GetManifestResourceStream('GuardianIcon')
$hash = [Security.Cryptography.SHA256]::Create()
try { $resourceHash = [BitConverter]::ToString($hash.ComputeHash($resource)).Replace('-','') }
finally { $resource.Dispose(); $hash.Dispose() }
. (Join-Path $release 'scripts\guardian-common.ps1')
if ($resourceHash -ne (Get-GuardianFileHash -Path (Join-Path $release 'desktop\assets\guardian-sky.png'))) { throw 'Embedded resource differs from the public icon.' }
if ((& (Join-Path $release 'responses-guardian.exe') --version) -ne $manifest.proxyVersion) { throw 'Built proxy version mismatch.' }
Update-GuardianChecksums -Root $release
$allowed = @($files + @('Guardian.exe','responses-guardian.exe','SHA256SUMS.txt'))
foreach ($file in Get-ChildItem -LiteralPath $release -Recurse -File -Force) {
    $relative = $file.FullName.Substring($release.Length + 1).Replace('\','/')
    if ($relative -notin $allowed) { throw ('Unexpected release file: ' + $relative) }
}

Add-Type -AssemblyName System.IO.Compression
function Write-PublicArchive {
    param([string]$Path,[string[]]$Paths)
    $stream = [IO.File]::Open($Path,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write)
    $zip = New-Object IO.Compression.ZipArchive($stream,[IO.Compression.ZipArchiveMode]::Create,$false)
    try {
        foreach ($relative in $Paths) {
            $entry = $zip.CreateEntry($packageName + '/' + $relative.Replace('\','/'),[IO.Compression.CompressionLevel]::Optimal)
            $archiveInputStream = [IO.File]::OpenRead((Join-Path $release $relative))
            $entryStream = $entry.Open()
            try { $archiveInputStream.CopyTo($entryStream) } finally { $entryStream.Dispose(); $archiveInputStream.Dispose() }
        }
    } finally { $zip.Dispose(); $stream.Dispose() }
}
Write-PublicArchive -Path $sourceZip -Paths $files
Write-PublicArchive -Path $windowsZip -Paths $allowed
$checksumLines = foreach ($archive in @($sourceZip,$windowsZip)) { (Get-GuardianFileHash -Path $archive) + '  ' + [IO.Path]::GetFileName($archive) }
[IO.File]::WriteAllLines((Join-Path $output ($packageName + '-SHA256SUMS.txt')),[string[]]$checksumLines,[Text.Encoding]::ASCII)
Write-Host ('Public source package: ' + $sourceZip)
Write-Host ('Public Windows package: ' + $windowsZip)
Write-Host 'Verified: explicit file allowlist, editorial checks, version agreement and public-icon-only embedded resources.'
