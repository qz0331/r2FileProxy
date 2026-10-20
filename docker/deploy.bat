@echo off
chcp 65001 >nul
setlocal enabledelayedexpansion

echo ==========================================
echo        R2 Proxy 一键部署 (Windows)
echo ==========================================

REM 检查 .env 文件
if not exist ".env" (
    echo 创建 .env 文件...
    copy .env.example .env >nul
    echo 请编辑 .env 文件填入你的 R2 配置，然后重新运行此脚本
    pause
    exit /b 1
)

REM 检查 Docker
where docker >nul 2>&1
if %errorlevel% neq 0 (
    echo 错误: 未安装 Docker Desktop
    pause
    exit /b 1
)

REM 构建并启动
echo 构建镜像...
docker-compose -f docker-compose.yml build

echo 启动服务...
docker-compose -f docker-compose.yml up -d

echo.
echo ==========================================
echo 部署完成!
echo 访问地址: http://localhost:8080
echo.
echo 常用命令:
echo   查看日志: docker-compose logs -f
echo   停止服务: docker-compose down
echo   重启服务: docker-compose restart
echo ==========================================
pause
