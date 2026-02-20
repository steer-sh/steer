@echo off
REM Steer CLI shim for Windows — dispatches to the correct platform binary.

set "SCRIPT_DIR=%~dp0"
set "PLUGIN_DIR=%SCRIPT_DIR%.."
set "BINARY=%PLUGIN_DIR%\bin\windows-amd64\steer.exe"

if not exist "%BINARY%" (
    echo steer: binary not found at %BINARY% >&2
    echo Run: cd %PLUGIN_DIR%\cli ^&^& bash build.sh >&2
    exit /b 1
)

"%BINARY%" %*
