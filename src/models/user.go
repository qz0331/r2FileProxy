package models

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"gorm.io/gorm"
)

// User 用户模型
type User struct {
	gorm.Model
	Username          string     `gorm:"uniqueIndex;size:64;not null" json:"username"`
	PasswordHash      string     `gorm:"size:256" json:"-"`
	APIKey            string     `gorm:"uniqueIndex;size:64" json:"api_key,omitempty"`
	APIKeyIPWhitelist string     `gorm:"size:2048" json:"api_key_ip_whitelist"` // JSON数组存储IP白名单
	UserType          string     `gorm:"size:16;not null;default:'web'" json:"user_type"`
	Role              string     `gorm:"size:16;not null;default:'user'" json:"role"`
	IsAdmin           bool       `gorm:"default:false" json:"is_admin"`
	LoginFailCount    int        `gorm:"default:0" json:"login_fail_count"`
	LoginLockedUntil  *time.Time `gorm:"index" json:"login_locked_until,omitempty"`
	Files             []File     `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"files,omitempty"`
}

// 用户类型常量
const (
	UserTypeWeb  = "web"
	UserTypeAPI  = "api"
	UserTypeBoth = "both"
)

// 角色常量
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// HasPassword 是否设置了密码
func (u *User) HasPassword() bool {
	return u.PasswordHash != ""
}

// HasAPIKey 是否设置了 API Key
func (u *User) HasAPIKey() bool {
	return u.APIKey != ""
}

// CanLoginByPassword 是否可以通过密码登录
func (u *User) CanLoginByPassword() bool {
	return u.UserType == UserTypeWeb || u.UserType == UserTypeBoth
}

// CanUseAPIKey 是否可以使用 API Key
func (u *User) CanUseAPIKey() bool {
	return u.UserType == UserTypeAPI || u.UserType == UserTypeBoth
}

// IsAdministrator 兼容旧的 is_admin 字段与新的 role 字段
func (u *User) IsAdministrator() bool {
	return u.IsAdmin || u.Role == RoleAdmin
}

// IsLoginLocked 是否被登录锁定
func (u *User) IsLoginLocked() bool {
	if u.LoginLockedUntil == nil {
		return false
	}
	return time.Now().Before(*u.LoginLockedUntil)
}

// LockLogin 锁定登录（12小时）
func (u *User) LockLogin() {
	lockDuration := 12 * time.Hour
	lockedUntil := time.Now().Add(lockDuration)
	u.LoginLockedUntil = &lockedUntil
}

// UnlockLogin 解锁登录
func (u *User) UnlockLogin() {
	u.LoginLockedUntil = nil
	u.LoginFailCount = 0
}

// IncrementLoginFail 增加登录失败计数，返回是否达到锁定阈值
func (u *User) IncrementLoginFail() bool {
	u.LoginFailCount++
	return u.LoginFailCount >= 5
}

// ResetLoginFail 重置登录失败计数
func (u *User) ResetLoginFail() {
	u.LoginFailCount = 0
}

// GetAPIKeyIPWhitelist 获取API Key IP白名单
func (u *User) GetAPIKeyIPWhitelist() []string {
	if u.APIKeyIPWhitelist == "" {
		return nil
	}
	var ips []string
	if err := json.Unmarshal([]byte(u.APIKeyIPWhitelist), &ips); err != nil {
		return nil
	}
	return ips
}

// SetAPIKeyIPWhitelist 设置API Key IP白名单
func (u *User) SetAPIKeyIPWhitelist(ips []string) error {
	if len(ips) == 0 {
		u.APIKeyIPWhitelist = ""
		return nil
	}
	data, err := json.Marshal(ips)
	if err != nil {
		return err
	}
	u.APIKeyIPWhitelist = string(data)
	return nil
}

// IsIPAllowed 检查IP是否在白名单中
func (u *User) IsIPAllowed(clientIP string) bool {
	whitelist := u.GetAPIKeyIPWhitelist()
	if len(whitelist) == 0 {
		return true // 白名单为空，允许所有IP
	}

	clientIPAddr := net.ParseIP(strings.TrimSpace(clientIP))
	if clientIPAddr == nil {
		return false
	}

	for _, allowed := range whitelist {
		// 支持 CIDR 格式，如 192.168.1.0/24
		if strings.Contains(allowed, "/") {
			_, ipNet, err := net.ParseCIDR(allowed)
			if err != nil {
				continue
			}
			if ipNet.Contains(clientIPAddr) {
				return true
			}
		} else {
			// 单个IP
			allowedIP := net.ParseIP(strings.TrimSpace(allowed))
			if allowedIP != nil && allowedIP.Equal(clientIPAddr) {
				return true
			}
		}
	}

	return false
}
