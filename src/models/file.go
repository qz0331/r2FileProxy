package models

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// File 文件模型
type File struct {
	gorm.Model
	UserID           uint            `gorm:"not null;index" json:"user_id"`
	TemporaryTokenID *uint           `gorm:"index" json:"temporary_token_id"`
	ShareTokenID     *uint           `gorm:"index" json:"share_token_id"`
	Key              string          `gorm:"size:512;not null;index" json:"key"`
	Filename         string          `gorm:"size:256" json:"filename"`
	Remark           string          `gorm:"size:256" json:"remark"`
	Size             int64           `gorm:"default:0" json:"size"`        // 原始文件大小
	StoredSize       int64           `gorm:"default:0" json:"stored_size"` // R2 中实际占用大小（压缩后可能更小）
	ContentType      string          `gorm:"size:128" json:"content_type"`
	ContentEncoding  string          `gorm:"size:32" json:"-"`
	ETag             string          `gorm:"size:64" json:"etag"`
	Status           string          `gorm:"size:16;default:'pending';index" json:"status"`
	DownloadCount    int             `gorm:"default:0" json:"download_count"`
	UploaderIP       string          `gorm:"size:64" json:"uploader_ip"`
	UploadExpiresAt  *time.Time      `gorm:"index" json:"upload_expires_at,omitempty"` // 上传签名过期时间
	User             User            `gorm:"foreignKey:UserID" json:"-"`
	TemporaryToken   *TemporaryToken `gorm:"foreignKey:TemporaryTokenID" json:"-"`
	ShareToken       *ShareToken     `gorm:"foreignKey:ShareTokenID" json:"-"`
}

// 文件状态常量
const (
	FileStatusPending   = "pending"
	FileStatusConfirmed = "confirmed"
	FileStatusDeleted   = "deleted"
	FileStatusFailed    = "failed"
)

// IsConfirmed 是否已确认
func (f *File) IsConfirmed() bool {
	return f.Status == FileStatusConfirmed
}

// IsPending 是否等待确认
func (f *File) IsPending() bool {
	return f.Status == FileStatusPending
}

// IsDeleted 是否已删除
func (f *File) IsDeleted() bool {
	return f.Status == FileStatusDeleted
}

// FormatSize 格式化文件大小
func (f *File) FormatSize() string {
	return FormatBytes(f.Size)
}

// FormatBytes 格式化字节数
func FormatBytes(size int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)

	switch {
	case size >= TB:
		return fmt.Sprintf("%.2f TB", float64(size)/TB)
	case size >= GB:
		return fmt.Sprintf("%.2f GB", float64(size)/GB)
	case size >= MB:
		return fmt.Sprintf("%.2f MB", float64(size)/MB)
	case size >= KB:
		return fmt.Sprintf("%.2f KB", float64(size)/KB)
	default:
		return fmt.Sprintf("%d B", size)
	}
}
