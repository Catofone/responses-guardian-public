param([string]$OutputDirectory = (Join-Path $PSScriptRoot 'assets'))

$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing
New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
$frames = @()
foreach ($size in @(16, 24, 32, 48, 64, 128, 256)) {
    $bitmap = New-Object System.Drawing.Bitmap($size, $size)
    $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
    $graphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
    $graphics.ScaleTransform(($size / 64.0), ($size / 64.0))
    $blue = New-Object System.Drawing.SolidBrush([System.Drawing.ColorTranslator]::FromHtml('#0096DA'))
    $white = New-Object System.Drawing.SolidBrush([System.Drawing.Color]::White)
    $yellow = New-Object System.Drawing.SolidBrush([System.Drawing.ColorTranslator]::FromHtml('#FFC94D'))
    $square = New-Object System.Drawing.Drawing2D.GraphicsPath
    $square.AddArc(2, 2, 30, 30, 180, 90)
    $square.AddArc(32, 2, 30, 30, 270, 90)
    $square.AddArc(32, 32, 30, 30, 0, 90)
    $square.AddArc(2, 32, 30, 30, 90, 90)
    $square.CloseFigure()
    $graphics.FillPath($blue, $square)
    $shield = New-Object System.Drawing.Drawing2D.GraphicsPath
    $shield.AddLines([System.Drawing.PointF[]]@((New-Object System.Drawing.PointF(32,10)),(New-Object System.Drawing.PointF(50,17)),(New-Object System.Drawing.PointF(48,35))))
    $shield.AddBezier(48,35,46.67,43,41.33,49.33,32,54)
    $shield.AddBezier(32,54,22.67,49.33,17.33,43,16,35)
    $shield.AddLine(16,35,14,17)
    $shield.CloseFigure()
    $graphics.FillPath($white, $shield)
    $pen = New-Object System.Drawing.Pen($blue,4)
    $pen.StartCap = $pen.EndCap = [System.Drawing.Drawing2D.LineCap]::Round
    $pen.LineJoin = [System.Drawing.Drawing2D.LineJoin]::Round
    $graphics.DrawLines($pen,[System.Drawing.PointF[]]@((New-Object System.Drawing.PointF(25,23)),(New-Object System.Drawing.PointF(25,32)),(New-Object System.Drawing.PointF(39,32)),(New-Object System.Drawing.PointF(39,40))))
    $graphics.FillEllipse($blue,21,19,8,8)
    $graphics.FillEllipse($yellow,35,36,8,8)
    $stream = New-Object System.IO.MemoryStream
    $bitmap.Save($stream,[System.Drawing.Imaging.ImageFormat]::Png)
    $frames += [pscustomobject]@{ Size = $size; Bytes = $stream.ToArray() }
    if ($size -eq 256) { $bitmap.Save((Join-Path $OutputDirectory 'guardian-sky.png'),[System.Drawing.Imaging.ImageFormat]::Png) }
    $stream.Dispose(); $pen.Dispose(); $shield.Dispose(); $square.Dispose()
    $yellow.Dispose(); $white.Dispose(); $blue.Dispose(); $graphics.Dispose(); $bitmap.Dispose()
}
$file = [System.IO.File]::Create((Join-Path $OutputDirectory 'guardian-sky.ico'))
$writer = New-Object System.IO.BinaryWriter($file)
try {
    $writer.Write([uint16]0); $writer.Write([uint16]1); $writer.Write([uint16]$frames.Count)
    $offset = 6 + 16 * $frames.Count
    foreach ($frame in $frames) {
        $dimension = if ($frame.Size -eq 256) { 0 } else { $frame.Size }
        $writer.Write([byte]$dimension); $writer.Write([byte]$dimension)
        $writer.Write([byte]0); $writer.Write([byte]0)
        $writer.Write([uint16]1); $writer.Write([uint16]32)
        $writer.Write([uint32]$frame.Bytes.Length); $writer.Write([uint32]$offset)
        $offset += $frame.Bytes.Length
    }
    foreach ($frame in $frames) { $writer.Write([byte[]]$frame.Bytes) }
} finally { $writer.Dispose(); $file.Dispose() }
