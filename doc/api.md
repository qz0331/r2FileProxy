# API 接口文档

> 校验方式、签名流程与前端实现保持一致。所有 `{basePath}` 指安全入口前缀（例如 `/file`），
> 未启用安全入口时为空。API 同时挂载在 `/api` 与 `{basePath}/api` 下。

## 1. 概述

### 1.1 基础信息

- **Content-Type**: `application/json`（上传接口除外）
- **字符编码**: `UTF-8`
- **健康检查**: `GET {basePath}/healthz`

### 1.2 认证方式

| 方式 | 用法 | 说明 |
|------|------|------|
| Session | 登录后自动携带 Cookie | Web 页面使用 |
| API Key | 请求头 `X-API-Key: sk_xxx` | 程序调用；支持 IP 白名单 |

> Session 与 API Key 可同时使用。API Key 用户的 Web 操作会在前端自动带上 `X-API-Key`。

### 1.3 通用响应格式

成功：

```json
{ "success": true, "data": { } }
```

失败：

```json
{ "success": false, "error": { "code": "ERROR_CODE", "message": "错误描述" } }
```

### 1.4 错误码

| 错误码 | HTTP | 说明 |
|--------|------|------|
| UNAUTHORIZED | 401 | 未认证 / 用户名密码错误 |
| ACCOUNT_LOCKED | 403 | 连续 5 次密码错误，锁定 12 小时 |
| IP_NOT_ALLOWED | 403 | IP 不在 API Key 白名单 |
| FORBIDDEN | 403 | 无权限（如非管理员） |
| NOT_FOUND | 404 | 资源不存在 |
| INVALID_REQUEST | 400 | 参数错误 |
| INVALID_FILENAME | 400 | 文件名含非法字符 |
| FILE_TOO_LARGE | 413 | 超过上传大小限制 |
| TOKEN_EXPIRED / TOKEN_REVOKED | 410 | 分享链接失效 |
| QUOTA_EXCEEDED | 403 | 分享次数/容量用尽 |
| INTERNAL_ERROR | 500 | 服务端错误 |

---

## 2. 认证

### 2.1 登录

`POST {basePath}/login?next=/可选跳转路径`

```json
{ "username": "user1", "password": "password123" }
```

响应：

```json
{ "success": true, "message": "登录成功", "must_change_password": false, "redirect": "" }
```

`next` 仅接受站内相对路径，其它会被忽略（防开放重定向）。

### 2.2 退出

`POST {basePath}/logout` → `{ "success": true }`

### 2.3 API Key

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/key` | 获取当前用户 API Key |
| POST | `/api/key/regenerate` | 重新生成（旧 Key 立即失效） |
| GET | `/api/key/ip-whitelist` | 读取 IP 白名单 |
| PUT | `/api/key/ip-whitelist` | 覆盖写入，`{ "ip_whitelist": ["10.0.0.0/8"] }` |

白名单为空表示不限制；支持 IPv4/IPv6/CIDR，最多 50 条。

---

## 3. 签名 URL

### 3.1 获取上传签名

`POST /api/sign/upload`

```json
{
  "filename": "report.pdf",
  "content_type": "application/pdf",
  "expires_in": 3600,
  "remark": "可选备注",
  "prefix": "可选存储前缀"
}
```

响应：

```json
{
  "success": true,
  "data": {
    "upload_url": "https://<account>.r2.cloudflarestorage.com/uploads/1/report_260101120000ab12.pdf?X-Amz-...",
    "put_url": "同上",
    "key": "uploads/1/report_260101120000ab12.pdf",
    "file_id": 12,
    "filename": "report.pdf",
    "remark": "可选备注",
    "expires_at": "2026-01-02T12:00:00+08:00",
    "expires_in": 3600,
    "max_upload_size": 2147483648,
    "confirm_url": "/api/files/12/confirm"
  }
}
```

- `expires_in` 秒，默认 86400，最大 604800（7 天）。
- 签名**不绑定 Content-Type**，避免浏览器自动加头导致 `SignatureDoesNotMatch`。
- 返回统一为 `put_url`/`upload_url` 两个同义字段，便于脚本使用。

### 3.2 获取下载签名

`POST /api/sign/download`

```json
{ "key": "uploads/1/report_260101120000ab12.pdf", "expires_in": 3600 }
```

响应包含 `download_url`、`filename`、`size`、`content_type`、`public_url`、`expires_at`。
若 R2 中对象已不存在，返回 `404 FILE_NOT_IN_R2` 并带 `can_delete: true`。

### 3.3 Sign PUT 链接（给其他设备 / 脚本直接上传）

#### 生成

`POST /api/sign/put`

```json
{
  "filename": "backup.zip",
  "content_type": "application/zip",
  "expires_in": 86400,
  "remark": "每周备份",
  "prefix": "backup"
}
```

响应额外包含：

| 字段 | 说明 |
|------|------|
| `method` | 固定 `PUT` |
| `curl` | 可直接复制执行的 curl 命令 |
| `mobile_upload_url` | 手机/平板可打开的点选上传页（`{basePath}/put?url=...`） |
| `instructions` | 使用步骤说明 |

上传完成后调用 `POST /api/files/{file_id}/confirm` 入库。

#### 列出待确认链接

`GET /api/sign/put` → 返回最近 50 个 `pending` 文件（含 `expires_at` / `expired` / `uploader_ip`）。

#### 重新签发

`POST /api/sign/put/{file_id}/regenerate` → 为同一 key 生成新的 24 小时 PUT 链接。

#### 删除未使用的上传链接

`DELETE /api/files/{file_id}` → 同时删除待确认记录；该 PUT 地址随即失效（R2 中本就没有对象）。
页面上的「待确认的 Sign PUT 链接」每条都带删除按钮，删除后列表与计数即时更新。

#### 手机点选上传页

- `GET {basePath}/put?url=<PUT地址>`：页面里可直接选择文件，由服务端中转 PUT 到 R2（规避 CORS）。
- `POST {basePath}/put/upload?url=<PUT地址>&filename=<文件名>`：同上接口形式。
- 仅允许 `https://*.r2.cloudflarestorage.com` 与 `https://*.amazonaws.com`，防止被当作任意请求代理。

---

## 4. 文件管理

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/files` | 列表，参数 `page` / `page_size`（≤100）/ `status` / `q`（文件名+备注模糊）/ `from` `to`（YYYY-MM-DD）/ `sort`（newest/oldest/name/size_desc/size_asc） |
| POST | `/api/files/{id}/confirm` | 确认上传，读取 R2 的 size/ETag/Content-Encoding |
| PUT | `/api/files/{id}/remark` | 更新备注，`{ "remark": "..." }` |
| DELETE | `/api/files/{id}` | 删除记录 + R2 对象 |
| POST | `/api/files/batch-delete` | 批量删除，`{ "ids": [1,2,3] }` |
| GET | `/api/files/{id}/download-url` | 5 分钟有效的下载链接 |
| GET | `/api/files/{id}/download` | 代理下载（上限 10 次，计入下载次数） |
| POST | `/api/upload?filename=x&remark=y&prefix=z&compress=0` | 代理上传（原始请求体即文件内容） |

列表响应：

```json
{
  "success": true,
  "data": {
    "files": [
      {
        "ID": 12,
        "filename": "report.pdf",
        "key": "uploads/1/report_260101120000ab12.pdf",
        "size": 102400,
        "stored_size": 20480,
        "status": "confirmed",
        "download_count": 0,
        "uploader_ip": "203.0.113.7",
        "CreatedAt": "2026-01-01T12:00:00+08:00"
      }
    ],
    "total": 1, "page": 1, "page_size": 20, "total_pages": 1
  }
}
```

### 4.1 存储用量口径

首页统计有两套来源：

| 指标 | 来源 | 说明 |
|------|------|------|
| 我的文件（记录统计） | 本地 `SUM(files.size)` | 当前账号的文件记录原始大小 (已排除 `deleted_at` 非空) |
| R2 实占 | 本地 `SUM(files.stored_size)` | 写入 R2 的真实字节（gzip 后更小） |
| R2 桶实际占用（官方） | `ListObjectsV2` | 遍历桶内所有对象累加 `ContentLength`，含外部工具写入的对象 |

官方统计结果在进程内缓存 2 分钟（`services.BucketUsage`），首次访问同步统计，
过期后由后台异步刷新，因此页面不会因为桶内对象多而变慢。

### 4.2 上传大小限制

| 项 | 默认 | 配置项 | 说明 |
|----|------|--------|------|
| 代理上传（首页 / 临时授权 / 分享页） | **2 GiB** | `MAX_UPLOAD_SIZE` | 单文件上限；超限返回 `413 FILE_TOO_LARGE` |
| URL 后台下载 | **2 GiB** | `MAX_DOWNLOAD_SIZE` | 下载任务上限 |
| Sign PUT 直传 | 无服务端限制 | - | 由 R2 自身的单对象上限决定（S3 API 单对象 ≤ 5 TiB） |

- 大小写法支持 `1024` / `10MB` / `2G`；
- 前端会在开始上传前调用 `GET {basePath}/put/info` 预校验，超限直接提示，不会白传；
- 服务端双重校验：先看 `Content-Length`（超限立即 413），流式写入时再用 `io.LimitReader` 兜底（防伪造长度/分块上传）。

```bash
# 查看当前生效上限
curl {BASE}/put/info
# {"success":true,"data":{"max_upload_size":2147483648,"human_max_size":"2.00 GB"}}
```

### 4.3 代理上传

`POST /api/upload?filename=big.zip`

- 请求体即文件二进制；`X-Content-Type` 头可指定 MIME。
- ≤16 MiB 单次 PUT，更大自动走分片上传（16 MiB/片，并发 4）。
- 文本类文件自动 gzip 压缩（`Content-Encoding: gzip`），压缩无收益时自动回退。
- 受 `MAX_UPLOAD_SIZE` 限制（默认 2 GiB）。

```bash
curl -X POST "http://localhost:8080/api/upload?filename=backup.zip" \
  -H "X-API-Key: sk_xxx" \
  -H "X-Content-Type: application/zip" \
  --data-binary @backup.zip
```

---

## 5. URL 后台下载任务

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/download-tasks` | 创建任务 `{ "url", "filename"?, "remark"? }` |
| GET | `/api/download-tasks` | 最近 50 个任务（含进度 `Downloaded`/`Progress`） |
| GET | `/api/download-tasks/{id}` | 单个任务，附带 `progress` |
| POST | `/api/download-tasks/{id}/cancel` | 取消执行中的任务 |
| DELETE | `/api/download-tasks/{id}` | 删除已结束任务 |
| DELETE | `/api/download-tasks` | 清空已结束任务 |

任务状态：`pending` → `downloading` → `completed` / `failed` / `cancelled`。

- 并发上限 3，单任务超时 6 小时，服务重启会把未完成任务标记为失败。
- 下载流直接写入 R2（临时文件 + 分片），不会把整个文件读进内存。
- 支持 GitHub 仓库地址，自动下载 zip（自动探测默认分支，回退 main/master）。
- 受 `MAX_DOWNLOAD_SIZE` 限制（默认 2 GiB）。

---

## 6. 临时授权（一次性分享）

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/temp-tokens` | 创建，`{ "description", "expires_in" (分钟, 默认30, 最大10080) }` |
| GET | `/api/temp-tokens` | 列表（含 `file_count`/`total_size`/`status`/`remaining_seconds`） |
| DELETE | `/api/temp-tokens/{id}` | 删除（文件保留，解除关联） |
| DELETE | `/api/temp-tokens` | 清空 |

公开接口（无需登录）：

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `{basePath}/temp/{token}` | 上传页面 |
| POST | `{basePath}/temp/{token}/sign/upload` | 获取上传签名 |
| POST | `{basePath}/temp/{token}/upload?filename=x` | 代理上传 |
| GET | `{basePath}/temp/{token}/files` | 该授权下的文件 |
| POST | `{basePath}/temp/{token}/files/{id}/confirm` | 确认上传 |
| GET | `{basePath}/temp/{token}/files/{id}/download-url` | 下载链接 |
| DELETE | `{basePath}/temp/{token}/files/{id}` | 删除文件 |
| POST/GET/DELETE | `{basePath}/temp/{token}/download-tasks[/{id}]` | URL 下载任务 |

---

## 7. 设备分享（长期链接）

适用于「其他设备 / 脚本」长期使用，链接保存在对方设备即可。

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/shares` | 创建 |
| GET | `/api/shares` | 列表 |
| PUT | `/api/shares/{id}` | 更新 / 撤销 |
| DELETE | `/api/shares/{id}` | 删除（文件保留） |

创建请求：

```json
{
  "name": "我的 iPad",
  "description": "上传照片",
  "mode": "upload",
  "prefix": "ipad",
  "expires_in": 1440,
  "max_uploads": 0,
  "max_total_size": 0,
  "max_file_size": 0
}
```

- `mode`: `upload`（仅上传）/ `both`（可上传 + 可查看下载）。
- `expires_in`: 分钟，默认 1440，最大 10080。
- `max_*`: 0 表示不限制，超限返回 `QUOTA_EXCEEDED`。
- 响应含 `share_url`、`status`、`used_count`、`used_size`、`curl`。

公开接口：

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `{basePath}/s/{token}` | 上传页面 |
| GET | `{basePath}/s/{token}/info` | 分享信息 |
| POST | `{basePath}/s/{token}/sign/upload` | 获取上传签名（直传 R2） |
| POST | `{basePath}/s/{token}/upload?filename=x` | 代理上传（推荐，规避 CORS） |
| GET | `{basePath}/s/{token}/files` | 文件列表（upload 模式仅返回本机 IP 上传的） |
| POST | `{basePath}/s/{token}/files/{id}/confirm` | 确认上传 |
| GET | `{basePath}/s/{token}/files/{id}/download-url` | 下载链接 |
| DELETE | `{basePath}/s/{token}/files/{id}` | 删除（upload 模式限本机上传） |
| POST/GET/DELETE | `{basePath}/s/{token}/download-tasks[/{id}]` | URL 下载任务 |

---

## 8. 用户管理（管理员）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/users` | 列表 |
| POST | `/api/users` | `{ "username", "password", "user_type" }` |
| DELETE | `/api/users/{id}` | 删除（不能删自己，也不能删除最后一个管理员） |

密码策略：≥15 位，必须含大小写字母、数字、特殊字符。

---

## 9. 使用示例

```bash
BASE=https://files.example.com
KEY=sk_xxxxxxxx

# 1. 生成 Sign PUT 链接
curl -s -X POST "$BASE/api/sign/put" \
  -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"filename":"backup.zip","expires_in":86400}'

# 2. 直接上传（upload_url 来自上一步）
curl -X PUT -T ./backup.zip "<upload_url>"

# 3. 确认入库
curl -X POST "$BASE/api/files/<file_id>/confirm" -H "X-API-Key: $KEY"

# 4. 获取下载链接
curl "$BASE/api/files/<file_id>/download-url" -H "X-API-Key: $KEY"

# 5. 创建设备分享
curl -s -X POST "$BASE/api/shares" \
  -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"name":"我的手机","expires_in":1440,"mode":"both"}'
```

Python 上传示例：

```python
import requests

url = "<upload_url>"
with open("backup.zip", "rb") as f:
    r = requests.put(url, data=f)
print(r.status_code)
```
