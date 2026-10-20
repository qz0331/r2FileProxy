package models

import (
	"time"

	"gorm.io/gorm"
)

// 分享类型常量
const (
	ShareModeUpload = "upload" // 仅允许上传
	ShareModeBoth   = "both"   // 允许上传与只读文件列表/下载
)

// ShareToken 设备分享令牌
//
// 与临时授权（TemporaryToken）不同，分享令牌面向“自己的其他设备”：
//   - 令牌保存在 localStorage，可重复使用直到过期或主动撤销；
//   - 保留上传人标识（UploaderIP / UploaderUA），便于区分不同设备；
//   - 可限制总上传字节数、单文件大小与单次令牌有效期。
type ShareToken struct {
	gorm.Model
	Token         string     `gorm:"uniqueIndex;size:64;not null" json:"token"`
	UserID        uint       `gorm:"not null;index" json:"user_id"`
	Name          string     `gorm:"size:64" json:"name"`
	Description   string     `gorm:"size:256" json:"description"`
	Mode          string     `gorm:"size:16;not null;default:'upload'" json:"mode"`
	Prefix        string     `gorm:"size:128" json:"prefix"`
	MaxUploads    int        `gorm:"default:0" json:"max_uploads"`    // 0 = 不限制次数
	MaxTotalSize  int64      `gorm:"default:0" json:"max_total_size"` // 0 = 不限制总大小
	MaxFileSize   int64      `gorm:"default:0" json:"max_file_size"`  // 0 = 不限制单文件
	UsedCount     int        `gorm:"default:0" json:"used_count"`
	UsedSize      int64      `gorm:"default:0" json:"used_size"`
	ExpiresAt     time.Time  `gorm:"not null;index" json:"expires_at"`
	LastUsedAt    *time.Time `gorm:"index" json:"last_used_at,omitempty"`
	LastIP        string     `gorm:"size:64" json:"last_ip"`
	LastUserAgent string     `gorm:"size:256" json:"last_user_agent"`
	Revoked       bool       `gorm:"default:false;index" json:"revoked"`
	User          User       `gorm:"foreignKey:UserID" json:"-"`
}

// IsExpired 是否已过期
func (s *ShareToken) IsExpired() bool {
	return s.Revoked || time.Now().After(s.ExpiresAt)
}

// Status 返回令牌状态
func (s *ShareToken) Status() string {
	if s.Revoked {
		return "revoked"
	}
	if s.IsExpired() {
		return "expired"
	}
	if s.MaxUploads > 0 && s.UsedCount >= s.MaxUploads {
		return "quota_exceeded"
	}
	if s.MaxTotalSize > 0 && s.UsedSize >= s.MaxTotalSize {
		return "quota_exceeded"
	}
	return "active"
}

// RemainingUploads 剩余可用次数，-1 表示不限制
func (s *ShareToken) RemainingUploads() int {
	if s.MaxUploads <= 0 {
		return -1
	}
	left := s.MaxUploads - s.UsedCount
	if left < 0 {
		return 0
	}
	return left
}

// RemainingSize 剩余可用字节数，-1 表示不限制
func (s *ShareToken) RemainingSize() int64 {
	if s.MaxTotalSize <= 0 {
		return -1
	}
	left := s.MaxTotalSize - s.UsedSize
	if left < 0 {
		return 0
	}
	return left
}

// CanUpload 是否允许继续上传
func (s *ShareToken) CanUpload() bool {
	return s.Status() == "active"
}

// GenerateShareToken 生成分享令牌
func GenerateShareToken() string {
	return "shr_" + randomHex(24)
}
