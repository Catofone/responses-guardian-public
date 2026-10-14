$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    gofmt -w ./cmd/responses-guardian
    if ($LASTEXITCODE -ne 0) { throw 'gofmt failed' }
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Tests failed' }
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }
    go build -trimpath -ldflags '-s -w' -o responses-guardian.next.exe ./cmd/responses-guardian
    if ($LASTEXITCODE -ne 0) { throw 'Build failed' }
    $hash = (Get-FileHash -LiteralPath 'responses-guardian.next.exe' -Algorithm SHA256).Hash
    Set-Content -LiteralPath 'responses-guardian.next.sha256' -Value $hash -Encoding ascii
    . (Join-Path $PSScriptRoot 'guardian-common.ps1')
    Update-GuardianChecksums -Root $root
    Write-Host 'Built and staged the new binary. Close Codex once before running .\scripts\activate-update.ps1.'
} finally {
    Pop-Location
}
