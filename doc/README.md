# R2 Proxy 文档目录

## 文档列表

| 文档 | 说明 |
|------|------|
| [R2 技术文档](./r2.md) | R2 Presigned URL 原理与实现 |
| [数据库设计](./database.md) | 数据表结构与模型定义 |
| [API 接口文档](./api.md) | REST API 接口定义 |
| [Web 界面流程](./web.md) | 前端交互流程与时序图 |

## 项目概览

**R2 Proxy** 是一个 Cloudflare R2 代理服务，支持：

- **Web 用户**：登录后通过签名 URL 直接上传/下载文件到 R2
- **API 用户**：通过 API Key 获取签名 URL，删除操作走代理
- **统一用户管理**：Web 用户和 API 用户共用一张表

## 技术栈

| 组件 | 技术 |
|------|------|
| 后端框架 | Gin |
| ORM | GORM |
| 数据库 | SQLite |
| 前端 | HTML + Tailwind CSS |
| R2 SDK | AWS SDK for Go v2 |

## 快速开始

```bash
# 复制环境变量
cp .env.example .env

# 编辑配置
vim .env

# 安装依赖
go mod tidy

# 运行服务
go run src/main.go
```
