# Web 界面流程

## 1. 页面列表

| 页面 | 路由 | 模板 | 认证 | 说明 |
|------|------|------|------|------|
| 登录 | `{basePath}/login` | login.html | 无需 | 左侧品牌介绍 + 右侧表单（窄屏隐藏左侧） |
| 概览 | `{basePath}/` | index.html | 需要 | 存储用量、上传（本地/URL/Sign PUT）、最近文件 |
| 全部文件 | `{basePath}/files` | files.html | 需要 | 模糊搜索（文件名+备注）、状态/日期/排序、批量操作 |
| 设备分享 | `{basePath}/shares` | shares.html | 需要 | 分享链接管理 + 待确认 PUT 链接 |
| 设置 | `{basePath}/settings` | settings.html | 需要 | 账号、API Key、IP 白名单、改密 |
| 用户 | `{basePath}/users` | users.html | 管理员 | 用户增删与 Key 查看 |
| 临时上传 | `{basePath}/temp/{token}` | temp_upload.html | 无需 | 一次性授权上传 |
| 设备分享页 | `{basePath}/s/{token}` | share.html | 无需 | 长期分享链接上传 |
| PUT 上传页 | `{basePath}/put?url=` | put.html | 无需 | 手机点选文件，走服务端中转 PUT |
| 错误 | - | error.html | - | 链接失效 / 过期 |

所有页面共用 `_components.html` 中的 `topbar` 与 `scripts` 模板。

## 2. 导航与响应式布局

| 断点 | 导航形态 |
|------|----------|
| > 1180px | 左侧固定侧边栏（244px，含品牌/菜单/用户/退出），内容区横向铺满 |
| 901–1180px | 侧边栏收窄为图标条（78px），hover 显示 title |
| ≤ 900px | 无顶部栏、无侧边栏：仅底部标签栏（概览/文件/分享/用户*/设置/主题，*仅管理员） |

- 内容区 `.page` 使用 `width:100%` + `padding: clamp(...)`，桌面端不留 1180px 空白；
- 表单类页面加 `.page-narrow`（940px）保证可读性；
- 退出登录收进「设置」页底部（低频操作），侧边栏与底部栏不再放退出按钮；
- 公开页（临时授权 / 分享 / PUT 上传）**没有任何顶部栏**，统一使用底部操作栏
  （`.bottomnav.public-nav`：主题 / 上传 / 文件 / 顶部），桌面与手机一致；
- 后台页面 HTML 上带 `<body class="app">`（CSS 用它区分是否保留左侧留白）；
- 刘海屏适配：`viewport-fit=cover` + `env(safe-area-inset-bottom)`，Toast 在移动端抬到底部栏之上。

## 3. 文件页搜索

- `q`：**文件名 + 备注** 双字段模糊匹配（`LIKE %q%`，已转义 `%`/`_`/`\`，避免通配符注入）；
- `status`：all / confirmed / pending / failed；
- `from` / `to`：按上传日期（含当天）过滤；
- `sort`：newest / oldest / name / size_desc / size_asc；
- `page_size`：20 / 50 / 100；
- 输入框停顿 520ms 自动提交，回车立即搜索；筛选条件以 chip 形式回显；
- 旧链接 `/?filename=x&share_id=1` 会 302 跳转到 `/files?...`，保持兼容。

## 4. 主题（日间 / 夜间）

- 三态切换：**日间 → 夜间 → 跟随系统**（循环），保存在 `localStorage['r2proxy-theme']`；
- 入口：侧边栏底部 / 移动端底部标签栏 / 登录页右上角 / 公开页底部栏；
- 防闪烁：每个页面 `<head>` 内联脚本在样式生效前写入 `<html data-theme="...">`；
- `auto` 不写 `data-theme`，交由 CSS 的 `prefers-color-scheme` 处理，系统切换会实时跟随；
- 颜色全部走 CSS 变量（`--bg` / `--surface` / `--text` / `--border` / `--hair` …），
  深色值在 `app.css` 末尾的 `[data-theme="dark"]` 与 `@media (prefers-color-scheme: dark)` 两处定义；
- JS API：`R2.theme.get() / set(mode) / toggle() / onChange(fn)`；
- `<meta name="theme-color">` 会随主题切换（移动端地址栏颜色）。

## 5. 前端实现约定

- **样式**：`src/static/app.css` 提供设计系统（CSS 变量、卡片、按钮、表格、弹窗、Toast 等），
  断点：`≤560`（手机）/ `≤820`（小屏平板、隐藏桌面导航）/ `≤1024`（平板）/ 桌面。
- **脚本**：`src/static/app.js` 提供 `R2.*` 工具：`request`（统一错误处理）、`toast`、
  `confirm`、`openModal/closeModal`、`copy`、`formatSize/formatTime/countdown`、
  `putFile/postFile`（XHR 上传进度）、`qrInto`（本地二维码生成，无 CDN 依赖）。
- **二维码**：`src/static/qrcode.min.js`（qrcode-generator，随二进制一起部署，离线可用）。
- **安全入口**：所有前端请求都带 `{basePath}` 前缀（模板变量 `.basePath`）；
  后端同时把 API 挂在 `/api` 与 `{basePath}/api`，脚本调用不受影响。

## 6. 上传流程

### 6.1 代理上传（页面默认，推荐）

```
Browser ──POST /api/upload?filename=x──▶ Proxy ──流式──▶ 临时文件 ──分片──▶ R2
        ◀──── 200 {file: confirmed} ────        （≤16MiB 单次 PUT；文本自动 gzip）
```

- 优点：不依赖 R2 CORS；可显示进度；支持 >5GiB 大文件（分片并发 4）。
- 文本类（`text/*`、json、js、xml、svg、sh 等）自动 gzip，压缩无收益会自动回退。
- 受 `MAX_UPLOAD_SIZE` 限制。

### 6.2 直传签名（API / 外部脚本）

```
1. POST /api/sign/upload          → upload_url + file_id
2. PUT  upload_url（文件主体）     → R2 直收
3. POST /api/files/{id}/confirm   → HeadObject 回填 size/ETag/编码
```

状态流转：

```
pending ──上传成功并确认──▶ confirmed ──删除──▶ （记录与对象一起删除）
   └──────确认时 R2 无对象──▶ failed ──重新确认/删除──▶ …
```

### 6.3 拖拽 / 多选

`index.html`、`temp_upload.html`、`share.html`、`put.html` 均支持 `dropzone`：
点击选择、拖拽、键盘 Enter/Space 触发；`put.html` 一次只处理一个文件（一个 PUT 链接对应一个对象）。

## 7. Sign PUT 链接（给其他设备使用）

两种用法：

1. **文件页 →「Sign PUT 链接」**：填写文件名/有效期 → 生成 `upload_url`、
   `curl` 命令、`mobile_upload_url`、二维码；
2. **设备分享页 →「新建分享」**：生成一个可长期使用的上传页链接（手机打开即可上传）。

手机使用路径：

- 扫描二维码 / 复制链接 → 打开 `{basePath}/put?url=<PUT地址>` →
  选择文件 → 前端 POST 到 `{basePath}/put/upload` →
  服务端转发 PUT 到 R2（仅允许 `*.r2.cloudflarestorage.com` / `*.amazonaws.com`）。
- 上传成功后回到文件页点击「确认上传」入库。

## 8. 下载流程

```
点击下载 → GET /api/files/{id}/download-url
        → 返回 5 分钟有效的 presigned GET（带 Content-Disposition，中文名用 RFC5987）
        → 弹窗展示链接 + 二维码，点「开始下载」新窗口打开
```

代理下载 `GET /api/files/{id}/download` 会流式透传，并累计 `download_count`（上限 10 次）。

## 9. 删除流程

单删 `DELETE /api/files/{id}`、批删 `POST /api/files/batch-delete`：

1. 校验归属；
2. 先删 R2 对象，失败则保留记录并返回错误（可重试）；
3. 成功则删除数据库记录。

## 10. URL 后台下载

1. `POST /api/download-tasks` 创建任务（立即返回 task_id）；
2. 后台 goroutine：解析 URL（GitHub 仓库→zip）→ 流式下载 → 临时文件 → R2 分片上传 → 写入 `files`；
3. 页面每 2.5s 轮询任务列表，展示进度条；完成时自动刷新文件列表。

## 11. 交互细节

| 场景 | 行为 |
|------|------|
| 表单校验 | 前端提示 + 后端二次校验（密码复杂度、文件名、URL） |
| 危险操作 | 统一样式确认弹窗（删除文件/用户/分享/清空） |
| 反馈 | 右下角（手机居中）Toast：成功 2.6s、失败 4.2s |
| 按钮加载 | `R2.setBusy(btn, true)` 禁用并显示进行中文案 |
| 空状态 | 统一 `empty` 组件，附带操作提示 |
| 无障碍 | 弹窗 `role="dialog"`、Esc 关闭、点击遮罩关闭、按钮 `aria-label` |
| 减少动画 | `prefers-reduced-motion` 下关闭过渡与动画 |

## 12. 响应式要点

| 断点 | 变化 |
|------|------|
| >1180px | 左侧完整侧边栏；卡片两/三栏；内容区横向铺满 |
| 901–1180px | 侧边栏收成 78px 图标条（hover 有 title） |
| ≤1024px | 登录页隐藏品牌侧栏（改为顶部小品牌）；三栏降两栏 |
| ≤900px | 侧边栏隐藏 → 仅底部标签栏（去掉了顶部栏，最大化内容高度）；表单单列；表格转卡片式 |
| ≤560px | 文件图标隐藏、操作按钮整行；弹窗底部弹出（sheet 风格）；hero 缩小内边距 |
| 刘海屏 | `viewport-fit=cover` + `env(safe-area-inset-bottom)` |
