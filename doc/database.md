# 数据库设计文档

## 1. 数据库选型

- **数据库**：SQLite
- **ORM**：GORM
- **文件位置**：`r2proxy.db`

## 2. 表结构设计

### 2.1 users 表

用户表，统一管理 Web 用户和 API 用户。

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| id | INTEGER | PRIMARY KEY AUTOINCREMENT | 主键 |
| username | VARCHAR(64) | UNIQUE NOT NULL | 用户名 |
| password_hash | VARCHAR(256) | NULL | Web 用户密码哈希（bcrypt） |
| api_key | VARCHAR(64) | UNIQUE NULL | API 用户密钥 |
| user_type | VARCHAR(16) | NOT NULL | 用户类型：web/api/both |
| is_admin | BOOLEAN | DEFAULT FALSE | 是否管理员 |
| created_at | DATETIME | NOT NULL | 创建时间 |
| updated_at | DATETIME | NOT NULL | 更新时间 |

**SQL**：
```sql
CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username VARCHAR(64) UNIQUE NOT NULL,
    password_hash VARCHAR(256),
    api_key VARCHAR(64) UNIQUE,
    user_type VARCHAR(16) NOT NULL,
    is_admin BOOLEAN DEFAULT FALSE,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE UNIQUE INDEX idx_users_username ON users(username);
CREATE UNIQUE INDEX idx_users_api_key ON users(api_key) WHERE api_key IS NOT NULL;
```

### 2.2 files 表

文件表，记录用户上传的文件信息。

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| id | INTEGER | PRIMARY KEY AUTOINCREMENT | 主键 |
| user_id | INTEGER | NOT NULL, FOREIGN KEY | 所属用户 |
| key | VARCHAR(512) | NOT NULL | R2 对象键 |
| filename | VARCHAR(256) | NULL | 原始文件名 |
| size | BIGINT | NULL | 文件大小（字节） |
| content_type | VARCHAR(128) | NULL | MIME 类型 |
| etag | VARCHAR(64) | NULL | R2 ETag |
| status | VARCHAR(16) | DEFAULT 'pending' | 状态 |
| created_at | DATETIME | NOT NULL | 创建时间 |
| updated_at | DATETIME | NOT NULL | 更新时间 |

**状态说明**：
| 状态 | 说明 |
|------|------|
| pending | 已获取签名 URL，等待上传确认 |
| confirmed | 上传成功已确认 |
| deleted | 已删除 |

**SQL**：
```sql
CREATE TABLE files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    key VARCHAR(512) NOT NULL,
    filename VARCHAR(256),
    size BIGINT,
    content_type VARCHAR(128),
    etag VARCHAR(64),
    status VARCHAR(16) DEFAULT 'pending',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX idx_files_user_id ON files(user_id);
CREATE INDEX idx_files_key ON files(key);
CREATE INDEX idx_files_status ON files(status);
```

## 3. GORM 模型定义

### 3.1 User 模型

```go
package models

import (
    "gorm.io/gorm"
)

type User struct {
    gorm.Model
    Username     string `gorm:"uniqueIndex;size:64;not null"`
    PasswordHash string `gorm:"size:256"`
    APIKey       string `gorm:"uniqueIndex;size:64"`
    UserType     string `gorm:"size:16;not null;default:'web'"`
    IsAdmin      bool   `gorm:"default:false"`
    Files        []File `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

// 用户类型常量
const (
    UserTypeWeb  = "web"
    UserTypeAPI  = "api"
    UserTypeBoth = "both"
)

// TableName 指定表名
func (User) TableName() string {
    return "users"
}
```

### 3.2 File 模型

```go
package models

import (
    "gorm.io/gorm"
)

type File struct {
    gorm.Model
    UserID      uint   `gorm:"not null;index"`
    Key         string `gorm:"size:512;not null;index"`
    Filename    string `gorm:"size:256"`
    Size        int64  `gorm:""`
    ContentType string `gorm:"size:128"`
    ETag        string `gorm:"size:64"`
    Status      string `gorm:"size:16;default:'pending';index"`
    User        User   `gorm:"foreignKey:UserID"`
}

// 文件状态常量
const (
    FileStatusPending   = "pending"
    FileStatusConfirmed = "confirmed"
    FileStatusDeleted   = "deleted"
)

// TableName 指定表名
func (File) TableName() string {
    return "files"
}
```

## 4. 数据库初始化

```go
package database

import (
    "r2proxy/src/models"
    "gorm.io/driver/sqlite"
    "gorm.io/gorm"
    "gorm.io/gorm/logger"
)

var DB *gorm.DB

func Init(dsn string) error {
    var err error
    DB, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{
        Logger: logger.Default.LogMode(logger.Info),
    })
    if err != nil {
        return err
    }

    // 自动迁移
    return DB.AutoMigrate(
        &models.User{},
        &models.File{},
    )
}
```

## 5. 查询示例

### 5.1 用户相关

```go
// 根据 API Key 查找用户
func GetUserByAPIKey(apiKey string) (*models.User, error) {
    var user models.User
    err := database.DB.Where("api_key = ?", apiKey).First(&user).Error
    return &user, err
}

// 根据用户名查找用户
func GetUserByUsername(username string) (*models.User, error) {
    var user models.User
    err := database.DB.Where("username = ?", username).First(&user).Error
    return &user, err
}
```

### 5.2 文件相关

```go
// 获取用户的文件列表
func GetUserFiles(userID uint, page, pageSize int) ([]models.File, int64, error) {
    var files []models.File
    var total int64

    offset := (page - 1) * pageSize

    query := database.DB.Model(&models.File{}).Where("user_id = ?", userID)
    query.Count(&total)

    err := query.Order("created_at DESC").
        Offset(offset).
        Limit(pageSize).
        Find(&files).Error

    return files, total, err
}

// 根据 key 获取文件
func GetFileByKey(userID uint, key string) (*models.File, error) {
    var file models.File
    err := database.DB.Where("user_id = ? AND key = ?", userID, key).First(&file).Error
    return &file, err
}
```

### 2.3 temp_tokens 表（临时授权）

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| id | INTEGER | PK | 主键 |
| token | VARCHAR(64) | UNIQUE NOT NULL | `tmp_` + 32 位 hex |
| user_id | INTEGER | NOT NULL, FK | 创建者 |
| description | VARCHAR(256) | NULL | 描述（作为上传文件默认备注） |
| expires_at | DATETIME | NOT NULL, INDEX | 过期时间 |
| used_count | INTEGER | DEFAULT 0 | 成功获取上传签名次数 |

### 2.4 share_tokens 表（设备分享 / Sign PUT）

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| id | INTEGER | PK | 主键 |
| token | VARCHAR(64) | UNIQUE NOT NULL | `shr_` + 48 位 hex |
| user_id | INTEGER | NOT NULL, FK | 分享者 |
| name | VARCHAR(64) | NULL | 分享名称 |
| description | VARCHAR(256) | NULL | 备注（作为上传文件默认备注） |
| mode | VARCHAR(16) | NOT NULL | `upload` / `both` |
| prefix | VARCHAR(128) | NULL | R2 存储前缀 |
| max_uploads | INTEGER | DEFAULT 0 | 0 = 不限制次数 |
| max_total_size | BIGINT | DEFAULT 0 | 0 = 不限制总容量 |
| max_file_size | BIGINT | DEFAULT 0 | 0 = 不限制单文件 |
| used_count | INTEGER | DEFAULT 0 | 已使用次数 |
| used_size | BIGINT | DEFAULT 0 | 已使用容量（原始字节） |
| expires_at | DATETIME | NOT NULL, INDEX | 过期时间 |
| last_used_at | DATETIME | NULL | 最近使用时间 |
| last_ip | VARCHAR(64) | NULL | 最近使用 IP |
| last_user_agent | VARCHAR(256) | NULL | 最近使用 UA |
| revoked | BOOLEAN | DEFAULT FALSE, INDEX | 是否已撤销 |

### 2.5 download_tasks 表（URL 后台下载）

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| id | INTEGER | PK | 主键 |
| user_id | INTEGER | NOT NULL, FK | 任务所属用户 |
| url | VARCHAR(2048) | NOT NULL | 源地址 |
| filename | VARCHAR(256) | NULL | 解析后的文件名 |
| custom_name | VARCHAR(256) | NULL | 用户指定文件名 |
| remark | VARCHAR(256) | NULL | 备注 |
| status | VARCHAR(16) | INDEX | pending/downloading/completed/failed/cancelled |
| file_id | INTEGER | NULL | 成功后关联的文件 |
| temp_token_id | INTEGER | NULL | 来源临时授权 |
| share_token_id | INTEGER | NULL | 来源设备分享 |
| error | VARCHAR(512) | NULL | 失败原因 |
| size | BIGINT | DEFAULT 0 | 总字节数（-1 表示未知，落库为 0） |
| downloaded | BIGINT | DEFAULT 0 | 已下载字节（用于进度） |
| content_type | VARCHAR(128) | NULL | MIME |

### 2.6 files 表补充字段

| 字段 | 说明 |
|------|------|
| share_token_id | 来自设备分享时关联的分享 ID |
| stored_size | R2 中实际占用字节数（gzip 后可能小于 `size`） |
| content_encoding | `gzip` 或空 |
| uploader_ip | 上传来源 IP（分享 upload 模式用于隔离） |
| upload_expires_at | 上传签名过期时间（用于「待确认链接」列表） |

> 迁移：启动时 `AutoMigrate` 自动补齐新字段；旧的 `is_admin` 会同步到 `role`；
> 服务重启会把遗留的 `pending`/`downloading` 任务标记为失败，避免永久卡住。
