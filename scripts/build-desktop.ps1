param([string]$OutputPath = (Join-Path (Split-Path -Parent $PSScriptRoot) 'Guardian.exe'))

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$desktop = Join-Path $root 'desktop'
$compiler = Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
if (-not (Test-Path -LiteralPath $compiler)) { throw 'The .NET Framework C# compiler is unavailable.' }
& (Join-Path $desktop 'build-icon.ps1')
$arguments = @('/nologo', '/target:winexe', '/platform:x64', '/optimize+', '/langversion:5', '/utf8output',
    ('/out:' + [System.IO.Path]::GetFullPath($OutputPath)),
    ('/win32icon:' + (Join-Path $desktop 'assets\guardian-sky.ico')),
    ('/win32manifest:' + (Join-Path $desktop 'app.manifest')),
    ('/resource:' + (Join-Path $desktop 'assets\guardian-sky.png') + ',GuardianIcon'),
    '/reference:System.Windows.Forms.dll', '/reference:System.Drawing.dll', '/reference:System.Web.Extensions.dll',
    (Join-Path $desktop 'Program.cs'), (Join-Path $desktop 'GuardianRuntime.cs'), (Join-Path $desktop 'SettingsHistory.cs'), (Join-Path $desktop 'PersonalSkin.cs'), (Join-Path $desktop 'ModernControls.cs'), (Join-Path $desktop 'GuardianForm.cs'))
& $compiler @arguments
if ($LASTEXITCODE -ne 0) { throw 'Desktop build failed.' }
Write-Host "Built Windows desktop manager: $OutputPath"
