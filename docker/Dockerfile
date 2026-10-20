# R2 Proxy Docker Image
# 纯 Go SQLite 驱动，无需 CGO

# ---------- 构建阶段 ----------
FROM golang:1.24-alpine AS builder

WORKDIR /build

RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories

COPY go.mod go.sum ./
RUN GOTOOLCHAIN=auto go mod download

COPY src/ ./src/
COPY scripts/ ./scripts/

# 同步静态资源版本号（内容 hash）
RUN apk add --no-cache bash coreutils >/dev/null 2>&1 || true \
    && bash scripts/version-assets.sh

# 编译（必须使用 -trimpath，避免泄露构建机路径）
RUN CGO_ENABLED=0 GOOS=linux GOTOOLCHAIN=auto \
    go build -trimpath -ldflags="-s -w" -o r2proxy ./src

# ---------- 运行阶段 ----------
FROM alpine:3.19

WORKDIR /app

RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
    && apk add --no-cache ca-certificates tzdata su-exec \
    && addgroup -g 101 -S app \
    && adduser -u 100 -S -G app app

COPY --from=builder /build/r2proxy /app/r2proxy
COPY docker/entrypoint.sh /app/entrypoint.sh
COPY src/views /app/src/views/
COPY src/static /app/src/static/

RUN chmod +x /app/r2proxy /app/entrypoint.sh \
    && mkdir -p /app/data \
    && chown -R app:app /app

ENV GIN_MODE=release \
    SERVER_PORT=18080 \
    DATABASE_URL=/app/data/r2proxy.db \
    TZ=Asia/Shanghai

EXPOSE 18080

# 入口脚本会在 root 下修正数据目录权限，然后降权到 app 用户执行
ENTRYPOINT ["/app/entrypoint.sh"]
CMD []
