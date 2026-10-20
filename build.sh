#!/bin/bash

# R2 Proxy 构建脚本

set -e

VERSION=${1:-"1.0.0"}
BUILD_TIME=$(date +"%Y-%m-%d %H:%M:%S")
LDFLAGS="-s -w -X main.Version=$VERSION -X main.BuildTime=$BUILD_TIME"

echo "=========================================="
echo "    R2 Proxy Build Script"
echo "    Version: $VERSION"
echo "=========================================="

# 更新静态资源版本号
./scripts/version-assets.sh

# 清理 release 目录（保留说明文件）
rm -rf release/windows release/linux release/*.tar.gz
mkdir -p release/windows release/linux

# 构建 Windows 版本
echo "Building Windows version..."
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="$LDFLAGS" -o release/windows/r2proxy.exe ./src

# 构建 Linux 版本
echo "Building Linux version..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$LDFLAGS" -o release/linux/r2proxy ./src

# 复制模板、静态资源与配置示例
for target in windows linux; do
    cp -r src/views "release/$target/"
    cp -r src/static "release/$target/"
    cp .env.example "release/$target/"
done

# 创建压缩包
echo "Creating archives..."
cd release
tar -czvf r2proxy-windows-amd64.tar.gz windows
tar -czvf r2proxy-linux-amd64.tar.gz linux

echo ""
echo "=========================================="
echo "Build completed!"
echo "Output: release/"
ls -la
echo "=========================================="
