@echo off
setlocal ENABLEDELAYEDEXPANSION

set ROOT_DIR=%~dp0..
set APP_NAME=ollama-openai-proxy
set DIST_DIR=%ROOT_DIR%\dist
if "%VERSION%"=="" set VERSION=dev
set LDFLAGS=-s -w -X main.version=%VERSION%

if not exist "%DIST_DIR%" mkdir "%DIST_DIR%"

echo building Windows amd64 binary...
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
go build -trimpath -ldflags "%LDFLAGS%" -o "%DIST_DIR%\%APP_NAME%-windows-amd64.exe" .\cmd\ollama-openai-proxy
if errorlevel 1 exit /b 1

echo building Linux amd64 binary...
set GOOS=linux
set GOARCH=amd64
go build -trimpath -ldflags "%LDFLAGS%" -o "%DIST_DIR%\%APP_NAME%-linux-amd64" .\cmd\ollama-openai-proxy
if errorlevel 1 exit /b 1

echo artifacts written to %DIST_DIR%

