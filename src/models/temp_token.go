package models

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"gorm.io/gorm"
)

// TemporaryToken 临时授权令牌
type TemporaryToken struct {
	gorm.Model
	Token       string    `gorm:"uniqueIndex;size:64;not null" json:"token"`
	UserID      uint      `gorm:"not null;index" json:"user_id"`
	Description string    `gorm:"size:256" json:"description"`
	ExpiresAt   time.Time `gorm:"not null;index" json:"expires_at"`
	UsedCount   int       `gorm:"default:0" json:"used_count"`
	User        User      `gorm:"foreignKey:UserID" json:"-"`
	Files       []File    `gorm:"foreignKey:TemporaryTokenID;constraint:OnDelete:SET NULL" json:"files,omitempty"`
}

// IsExpired 是否已过期
func (t *TemporaryToken) IsExpired() bool {
	return time.Now().After(t.ExpiresAt)
}

// RemainingTime 剩余时间
func (t *TemporaryToken) RemainingTime() time.Duration {
	if t.IsExpired() {
		return 0
	}
	return time.Until(t.ExpiresAt)
}

// GenerateToken 生成随机 token
func GenerateToken() string {
	return "tmp_" + randomHex(16)
}

// randomHex 生成 n 字节的随机十六进制字符串
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand 不可用: " + err.Error())
	}
	return hex.EncodeToString(b)
}
