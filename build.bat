@echo off
chcp 65001 >nul
setlocal enabledelayedexpansion

REM R2 Proxy 构建脚本 (Windows)

set VERSION=%1
if "%VERSION%"=="" set VERSION=1.0.0

for /f "tokens=*" %%i in ('powershell -Command "Get-Date -Format 'yyyy-MM-dd HH:mm:ss'"') do set BUILD_TIME=%%i

echo ==========================================
echo     R2 Proxy Build Script
echo     Version: %VERSION%
echo ==========================================

REM 清理 release 目录
if exist release\windows rmdir /s /q release\windows
if exist release\linux rmdir /s /q release\linux
mkdir release\windows
mkdir release\linux

REM 复制 views 与 static 目录
echo Copying assets...
xcopy /s /e /y src\views release\windows\views\
xcopy /s /e /y src\views release\linux\views\
xcopy /s /e /y src\static release\windows\static\
xcopy /s /e /y src\static release\linux\static\

REM 复制配置文件
copy .env.example release\windows\
copy .env.example release\linux\

REM 构建 Windows 版本
echo Building Windows version...
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
go build -trimpath -ldflags="-s -w" -o release\windows\r2proxy.exe .\src

REM 构建 Linux 版本
echo Building Linux version...
set GOOS=linux
go build -trimpath -ldflags="-s -w" -o release\linux\r2proxy .\src

REM 重置环境
set GOOS=windows

echo.
echo ==========================================
echo Build completed!
echo Output: release\
dir release
echo ==========================================
pause
