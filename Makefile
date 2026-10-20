.PHONY: run build clean test release docker-build docker-up docker-down

# 运行开发服务器
run:
	go run src/main.go

# 更新静态资源版本号（改了 css/js 后执行）
assets:
	./scripts/version-assets.sh

# 构建当前平台
build: assets
	go build -trimpath -ldflags="-s -w" -o r2proxy.exe ./src

# 运行测试
test:
	go test ./src/...

# 清理
clean:
	rm -f r2proxy.exe
	rm -f r2proxy.db
	rm -rf release/windows release/linux
	rm -f release/*.tar.gz

# 打包发布版本 (Windows + Linux)
release:
	./build.sh

# Windows 用户使用
release-win:
	build.bat

# Docker 构建
docker-build:
	cd docker && docker-compose build

# Docker 启动
docker-up:
	cd docker && docker-compose up -d

# Docker 停止
docker-down:
	cd docker && docker-compose down

# Docker 查看日志
docker-logs:
	cd docker && docker-compose logs -f

# Docker 完整部署
docker-deploy: docker-build docker-up
	@echo "部署完成! 访问 http://localhost:8080"
