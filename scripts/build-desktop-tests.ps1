param(
    [Parameter(Mandatory = $true)][string]$DesktopExecutable,
    [Parameter(Mandatory = $true)][string]$OutputPath
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$compiler = Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
& $compiler /nologo /target:exe /platform:x64 /langversion:5 /utf8output `
    (('/out:') + [System.IO.Path]::GetFullPath($OutputPath)) `
    (('/win32manifest:') + (Join-Path $root 'desktop\app.manifest')) `
    (('/reference:') + [System.IO.Path]::GetFullPath($DesktopExecutable)) `
    /reference:System.Windows.Forms.dll /reference:System.Drawing.dll /reference:System.Web.Extensions.dll `
    (Join-Path $root 'desktop\GuardianApp.Tests.cs')
if ($LASTEXITCODE -ne 0) { throw 'Desktop UI test build failed.' }
