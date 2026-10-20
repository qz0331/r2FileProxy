# R2 Proxy

一个 Cloudflare R2 代理服务：Web 管理界面 + API Key 接口 + 临时授权 + **设备分享（Sign PUT 链接）**。

- 直连 R2 的预签名上传/下载，不占用服务器带宽
- 大文件代理上传自动分片（16 MiB/片，并发 4），文本类自动 gzip
- URL 后台下载（支持 GitHub 仓库自动打包 zip），流式写盘，不占内存
- 前端零外部依赖（自研设计系统 + 本地二维码库），桌面左侧边栏 / 手机底部标签栏，适配各种屏幕
- 日间 / 夜间 / 跟随系统三种主题，深色模式可随系统实时切换
- 安全入口（隐藏后台）、API Key IP 白名单、登录失败锁定、SSRF 防护

---

## 功能特性

### 核心

| 功能 | 说明 |
|------|------|
| Web 管理 | 登录后在浏览器中上传、下载、备注、批量删除文件 |
| API 接口 | `X-API-Key` 认证，支持 IP 白名单（CIDR） |
| 临时授权 | 生成短效链接（默认 30 分钟），他人可上传且文件归属自己 |
| **设备分享** | 长期链接（默认 24 小时，最长 7 天），手机/平板/脚本都能上传；可限制次数、总容量、单文件大小 |
| **文件搜索** | 独立文件页：文件名 + 备注模糊搜索、状态/日期筛选、大小与时间排序、批量操作 |
| **设备分享页可直接生成 PUT 链接** | 「生成 PUT 链接」按钮 → 表单 → 链接/curl/二维码/手机上传页，一步到位 |
| **待确认链接可删除** | 未使用的 Sign PUT 链接支持单条重签 / 确认 / 删除，删除后地址立即失效 |
| **Sign PUT 链接** | 直接生成单个文件的 PUT 地址 + curl 命令 + 二维码 + 手机点选上传页 |

### 文件与上传

- 三种上传方式：浏览器代理上传（带进度）、R2 直传签名、Sign PUT 链接
- 自动分片上传（>16 MiB）与 gzip 压缩（文本类，压缩无收益自动回退）
- 文件名过滤（`..`、`/`、`\`、控制字符），UTF-8 下载名（RFC 5987）
- 文件状态：待确认 / 已确认 / 失败；支持批量确认、批量删除
- 备注、分页（10/20/50/100）、按状态与文件名筛选

### 安全

- 密码策略：≥15 位且含大小写、数字、特殊字符
- 连续 5 次密码错误锁定 12 小时
- API Key 可设置 IP 白名单（IPv4/IPv6/CIDR）
- 安全入口 `SECURITY_ENTRY`（非法路径返回伪装页）
- 反代信任默认仅 `127.0.0.1`/`::1`，避免伪造 `X-Forwarded-For`
- URL 下载拒绝内网/localhost 与带认证信息的地址（SSRF 防护）
- PUT 中继仅允许 `*.r2.cloudflarestorage.com` / `*.amazonaws.com`

---

## 快速开始

### 方式一：直接运行

```bash
cp .env.example .env      # 填入 R2 配置
go mod tidy
make run                  # 或 go run src/main.go
```

### 方式二：Docker（推荐）

```bash
cd docker
cp ../.env.example .env   # 填入 R2 配置
docker compose up -d --build
```

> 仓库根目录的 `/data/r2proxy/docker-compose.yml` 也可以直接使用：
> 它以 `./r2proxy` 为构建上下文，使用 host 网络与 `docker_r2proxy-data` 数据卷。

### 方式三：发布包

```bash
make release        # Linux/macOS 打包 Windows + Linux
build.bat           # Windows
```

输出在 `release/windows`、`release/linux`，包含二进制、`views/`、`static/` 与 `.env.example`。

---

## 配置

| 变量 | 默认 | 说明 |
|------|------|------|
| `SERVER_PORT` | 8080 | 监听端口 |
| `SERVER_SECRET` | - | **必填**，Session 签名密钥（`openssl rand -hex 32`） |
| `SECURE_COOKIE` | false | HTTPS 部署时设为 true |
| `SECURITY_ENTRY` | 空 | 安全入口，如 `file` → 后台在 `/{entry}` 下 |
| `PUBLIC_BASE_URL` | 空 | 对外地址，用于生成分享链接（留空按请求推断） |
| `TRUSTED_PROXIES` | `127.0.0.1,::1` | 信任的反代地址，`*` 表示全部 |
| `R2_ACCOUNT_ID` | - | R2 账户 ID |
| `R2_ACCESS_KEY_ID` | - | R2 Access Key |
| `R2_SECRET_ACCESS_KEY` | - | R2 Secret Key |
| `R2_BUCKET_NAME` | - | 存储桶名 |
| `R2_PUBLIC_URL` | 空 | 公开访问域名（可选，提供额外的公开链接） |
| `MAX_DOWNLOAD_SIZE` | 2G | 单个 URL 下载任务上限 |
| `MAX_UPLOAD_SIZE` | 2G | 代理上传 / 分享上传单文件上限 |
| `DATABASE_URL` | r2proxy.db | SQLite 路径（Docker 中为 `/app/data/r2proxy.db`） |

大小支持 `1024`、`10MB`、`2G` 等写法。默认单个文件上限 **2 GiB**，
首页/分享页上传框会显示当前上限并在上传前预校验；服务端同时按 `Content-Length` 与流式读取双重拦截。

---

## 存储用量是怎么算的

首页顶部有两组数字，口径不同：

| 指标 | 数据来源 | 说明 |
|------|----------|------|
| **我的文件（记录统计）** | 本地 SQLite `SUM(files.size)` | 按你账号的文件记录汇总的**原始大小**，不含已删除记录 |
| └ R2 实占 | 本地 `SUM(files.stored_size)` | 写入 R2 的真实字节数（文本类 gzip 后可能更小） |
| **R2 桶实际占用（官方）** | `R2 ListObjectsV2` 遍历对象累加 | 直接问 R2 的**真实用量**，包含手工上传/外部工具写入的对象，以及不在本服务记录里的文件 |

- 官方用量每 2 分钟刷新一次（首次打开页面会同步统计，之后后台静默更新）；
- 桶内对象很多时首次统计会有 1~2 秒延迟，页面会显示“统计中”；
- 想让数字立刻刷新，删除文件后重新加载页面即可（缓存 2 分钟自动过期）。

## 管理员账号

首次启动且数据库为空时自动创建管理员：

| 配置 | 说明 |
|------|------|
| `ADMIN_USERNAME` | 管理员用户名，默认 `admin` |
| `ADMIN_PASSWORD` | 管理员密码；**不设置则自动生成 24 位随机密码并打印到启动日志一次** |

```bash
# 推荐：用环境变量指定
ADMIN_USERNAME=alice ADMIN_PASSWORD='换成你自己的强密码' ./r2proxy
```

> 密码要求 ≥15 位且包含大小写字母、数字、特殊字符。
> 若未指定 `ADMIN_PASSWORD`，请从启动日志中复制随机密码，并在首次登录后立即修改。

## 设备分享 / Sign PUT 使用

### 1. 生成 Sign PUT 链接（脚本 / 其他设备）

```bash
curl -X POST https://your-domain/api/sign/put \
  -H "X-API-Key: sk_xxx" -H "Content-Type: application/json" \
  -d '{"filename":"backup.zip","expires_in":86400}'

# 用返回的 upload_url 直接上传
curl -X PUT -T ./backup.zip "<upload_url>"

# 确认入库
curl -X POST https://your-domain/api/files/<file_id>/confirm -H "X-API-Key: sk_xxx"
```

返回里还包含：

- `curl`：可直接复制的命令
- `mobile_upload_url`：手机/平板打开后可点选文件的页面（规避 R2 CORS）
- `instructions`：步骤说明

### 2. 生成设备分享链接（长期、可限额）

```bash
curl -X POST https://your-domain/api/shares \
  -H "X-API-Key: sk_xxx" -H "Content-Type: application/json" \
  -d '{"name":"我的 iPad","expires_in":1440,"mode":"both","max_uploads":50}'
```

返回的 `share_url` 形如 `https://your-domain/s/shr_xxx`，对方打开即可上传（无需登录），
文件归属分享者。`mode` 为 `both` 时对方还能看到/下载分享内文件。

---

## 常用命令

```bash
make run            # 开发运行
make test           # go test ./src/...
make build          # 当前平台构建（-trimpath）
make release        # 打包 Windows + Linux
make docker-up      # docker compose 启动
make docker-logs    # 查看日志
```

**编译必须使用 `-trimpath`**，否则会泄露构建机目录结构。

---

## 项目结构

```
r2proxy/
├── src/
│   ├── main.go
│   ├── config/          # 环境变量解析与校验
│   ├── models/          # User / File / TemporaryToken / ShareToken / DownloadTask
│   ├── controllers/     # 各页面与 API
│   ├── middleware/      # 认证、安全入口前缀
│   ├── services/        # R2 客户端、分片上传、流式下载、任务调度
│   ├── database/        # GORM + SQLite 初始化与迁移
│   ├── scripts/         # 管理员初始化
│   ├── routes/          # 路由注册
│   ├── views/           # HTML 模板
│   └── static/          # app.css / app.js / qrcode.min.js
├── docker/              # Dockerfile / compose / 部署脚本
├── doc/                 # API、数据库、R2 原理、Web 流程
└── build.sh / build.bat / Makefile
```

---

## 文档

- [API 接口文档](doc/api.md)
- [Web 界面流程](doc/web.md)
- [数据库设计](doc/database.md)
- [R2 技术文档](doc/r2.md)

---

## 安全建议

1. 首次登录立即修改默认密码；
2. `SERVER_SECRET` 使用随机值，HTTPS 部署开启 `SECURE_COOKIE=true`；
3. 为 API Key 配置 IP 白名单；
4. 生产环境开启安全入口，并只信任实际的反向代理地址；
5. 定期重新生成 API Key，及时删除不再使用的分享链接。
