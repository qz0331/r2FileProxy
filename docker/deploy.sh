#!/bin/bash

# R2 Proxy 一键部署脚本

set -e

echo "=========================================="
echo "       R2 Proxy 一键部署"
echo "=========================================="

# 检查 .env 文件
if [ ! -f ".env" ]; then
    echo "创建 .env 文件..."
    cp .env.example .env
    echo "请编辑 .env 文件填入你的 R2 配置，然后重新运行此脚本"
    exit 1
fi

# 检查 Docker
if ! command -v docker &> /dev/null; then
    echo "错误: 未安装 Docker"
    exit 1
fi

if ! command -v docker-compose &> /dev/null && ! docker compose version &> /dev/null; then
    echo "错误: 未安装 docker-compose"
    exit 1
fi

# 构建并启动
echo "构建镜像..."
docker-compose -f docker-compose.yml build

echo "启动服务..."
docker-compose -f docker-compose.yml up -d

echo ""
echo "=========================================="
echo "部署完成!"
echo "访问地址: http://localhost:8080"
echo ""
echo "常用命令:"
echo "  查看日志: docker-compose logs -f"
echo "  停止服务: docker-compose down"
echo "  重启服务: docker-compose restart"
echo "=========================================="
