package models

import (
	"net/url"
	"strings"

	"gorm.io/gorm"
)

// ShortURL 返回便于展示的短地址（host + 路径），完整地址仍在 URL 字段
func (t *DownloadTask) ShortURL() string {
	raw := strings.TrimSpace(t.URL)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		if len(raw) > 60 {
			return raw[:57] + "…"
		}
		return raw
	}
	path := u.Path
	if len(path) > 48 {
		path = path[:45] + "…"
	}
	return u.Host + path
}

// DownloadTask 下载任务
type DownloadTask struct {
	gorm.Model
	UserID       uint            `gorm:"not null;index"`
	URL          string          `gorm:"size:2048;not null"`
	Filename     string          `gorm:"size:256"`
	Remark       string          `gorm:"size:256"`
	CustomName   string          `gorm:"size:256"`
	Status       string          `gorm:"size:16;default:'pending';index"` // pending, downloading, completed, failed, cancelled
	FileID       *uint           `gorm:"index"`                           // 成功后关联的文件ID
	TempTokenID  *uint           `gorm:"index"`                           // 来自临时授权时关联的令牌ID
	ShareTokenID *uint           `gorm:"index"`                           // 来自设备分享时关联的令牌ID
	Error        string          `gorm:"size:512"`                        // 失败原因
	Size         int64           `gorm:"default:0"`
	Downloaded   int64           `gorm:"default:0"` // 已下载字节数（用于进度显示）
	ContentType  string          `gorm:"size:128"`
	User         User            `gorm:"foreignKey:UserID"`
	File         *File           `gorm:"foreignKey:FileID"`
	TempToken    *TemporaryToken `gorm:"foreignKey:TempTokenID"`
	ShareToken   *ShareToken     `gorm:"foreignKey:ShareTokenID"`
}

// DownloadTaskStatus 下载任务状态
const (
	TaskStatusPending     = "pending"
	TaskStatusDownloading = "downloading"
	TaskStatusCompleted   = "completed"
	TaskStatusFailed      = "failed"
	TaskStatusCancelled   = "cancelled"
)

// FormatSize 格式化文件大小
func (t *DownloadTask) FormatSize() string {
	return FormatBytes(t.Size)
}

// Progress 返回 0-100 的进度，未知时返回 -1
func (t *DownloadTask) Progress() int {
	if t.Status == TaskStatusCompleted {
		return 100
	}
	if t.Size <= 0 || t.Downloaded <= 0 {
		return -1
	}
	p := int(t.Downloaded * 100 / t.Size)
	if p > 100 {
		p = 100
	}
	return p
}
