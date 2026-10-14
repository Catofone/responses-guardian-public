@echo off
setlocal EnableExtensions DisableDelayedExpansion
title Responses Guardian
"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\launch-guardian.ps1"
set "guardianLauncherExit=%errorlevel%"
if "%guardianLauncherExit%"=="0" exit /b 0
echo.
echo Responses Guardian could not start or open the control panel.
echo Review the error above before closing this window.
pause
exit /b %guardianLauncherExit%
