#!/bin/sh
# 容器入口：以 root 启动时先把数据目录交给 app 用户，再降权运行主程序。
# 这样既能让已有数据卷（可能属于 root）正常工作，又不会让主进程以 root 运行。
set -e

APP_UID=100
APP_GID=101
DATA_DIR="${DATA_DIR:-/app/data}"

if [ "$(id -u)" = "0" ]; then
    mkdir -p "$DATA_DIR"
    # 仅在属主不正确时递归修正，避免每次启动都遍历大目录
    if [ "$(stat -c '%u:%g' "$DATA_DIR" 2>/dev/null)" != "$APP_UID:$APP_GID" ]; then
        chown -R "$APP_UID:$APP_GID" "$DATA_DIR" || true
    fi
    if command -v su-exec >/dev/null 2>&1; then
        exec su-exec "$APP_UID:$APP_GID" /app/r2proxy "$@"
    fi
    exec setpriv --reuid="$APP_UID" --regid="$APP_GID" --init-groups /app/r2proxy "$@"
fi

exec /app/r2proxy "$@"
