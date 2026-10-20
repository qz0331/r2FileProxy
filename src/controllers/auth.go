package controllers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"r2proxy/src/config"
	"r2proxy/src/database"
	"r2proxy/src/middleware"
	"r2proxy/src/models"
	"strings"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// LoginPage 登录页面
func LoginPage(c *gin.Context) {
	if middleware.GetCurrentUser(c) != nil {
		c.Redirect(http.StatusFound, middleware.BasePathOf(c)+"/")
		return
	}
	c.HTML(http.StatusOK, "login.html", gin.H{
		"title":    "登录",
		"basePath": middleware.BasePathOf(c),
		"next":     safeNextPath(c.Query("next")),
	})
}

// SettingsPage 设置页面
func SettingsPage(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	c.HTML(http.StatusOK, "settings.html", gin.H{
		"title":         "设置",
		"user":          webUser(user),
		"active":        "settings",
		"basePath":      middleware.BasePathOf(c),
		"publicBaseURL": config.AppConfig.PublicBaseURL,
	})
}

// LoginRequest 登录请求
type LoginRequest struct {
	Username string `json:"username" binding:"required,min=3,max=64"`
	Password string `json:"password" binding:"required,min=6"`
}

// Login 登录
func Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "INVALID_REQUEST",
				"message": "请求参数错误",
			},
		})
		return
	}

	var user models.User
	if err := database.DB.Where("username = ?", strings.TrimSpace(req.Username)).First(&user).Error; err != nil {
		unauthorized(c, "用户名或密码错误")
		return
	}

	// 锁定检查
	if user.IsLoginLocked() {
		remaining := time.Until(*user.LoginLockedUntil)
		hours := int(remaining.Hours())
		minutes := int(remaining.Minutes()) % 60
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "ACCOUNT_LOCKED",
				"message": fmt.Sprintf("账号已被锁定，请 %d 小时 %d 分钟后再试", hours, minutes),
			},
		})
		return
	}

	// 锁定已过期则自动解锁
	if user.LoginLockedUntil != nil && time.Now().After(*user.LoginLockedUntil) {
		user.UnlockLogin()
		database.DB.Save(&user)
	}

	if !user.HasPassword() || !user.CanLoginByPassword() {
		unauthorized(c, "此账号不支持密码登录")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		if user.IncrementLoginFail() {
			user.LockLogin()
			database.DB.Save(&user)
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "ACCOUNT_LOCKED",
					"message": "连续登录失败5次，账号已被锁定12小时",
				},
			})
			return
		}
		database.DB.Save(&user)
		unauthorized(c, fmt.Sprintf("用户名或密码错误，还剩 %d 次尝试机会", 5-user.LoginFailCount))
		return
	}

	user.ResetLoginFail()
	database.DB.Save(&user)

	// 默认密码提醒：ADMIN_PASSWORD 未改过时提示（仅比较环境变量里的值，不含硬编码口令）
	mustChangePassword := false
	if user.IsAdministrator() {
		if initial := strings.TrimSpace(os.Getenv("ADMIN_PASSWORD")); initial != "" {
			if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(initial)) == nil {
				mustChangePassword = true
			}
		}
	}

	session := sessions.Default(c)
	session.Set("user_id", user.ID)
	session.Set("login_time", time.Now().Unix())
	if err := session.Save(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "创建会话失败"},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":              true,
		"message":              "登录成功",
		"must_change_password": mustChangePassword,
		"redirect":             safeNextPath(c.Query("next")),
	})
}

// Logout 退出登录
func Logout(c *gin.Context) {
	session := sessions.Default(c)
	session.Clear()
	_ = session.Save()

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func unauthorized(c *gin.Context, message string) {
	c.JSON(http.StatusUnauthorized, gin.H{
		"success": false,
		"error": gin.H{
			"code":    "UNAUTHORIZED",
			"message": message,
		},
	})
}

// safeNextPath 只允许站内相对路径，防止开放重定向
func safeNextPath(next string) string {
	if next == "" {
		return ""
	}
	u, err := url.Parse(next)
	if err != nil || u.IsAbs() || u.Host != "" || strings.HasPrefix(next, "//") {
		return ""
	}
	if !strings.HasPrefix(u.Path, "/") {
		return ""
	}
	return u.RequestURI()
}

// GetAPIKey 获取 API Key
func GetAPIKey(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"api_key": user.APIKey,
		},
	})
}

// RegenerateAPIKey 重新生成 API Key
func RegenerateAPIKey(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	newKey := generateAPIKey()

	user.APIKey = newKey
	if err := database.DB.Save(user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "生成 API Key 失败"},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"api_key": newKey},
	})
}

// generateAPIKey 生成 API Key
func generateAPIKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand 不可用: " + err.Error())
	}
	return "sk_" + hex.EncodeToString(b)
}

// ChangePasswordRequest 修改密码请求
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=15"`
}

// validatePasswordComplexity 验证密码复杂度
// 要求：15位以上，包含大写、小写、数字、特殊字符
func validatePasswordComplexity(password string) error {
	if len(password) < 15 {
		return fmt.Errorf("密码长度必须至少15位")
	}
	if len(password) > 128 {
		return fmt.Errorf("密码长度不能超过128位")
	}

	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, r := range password {
		switch {
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= '0' && r <= '9':
			hasDigit = true
		default:
			hasSpecial = true
		}
	}

	var missing []string
	if !hasUpper {
		missing = append(missing, "大写字母")
	}
	if !hasLower {
		missing = append(missing, "小写字母")
	}
	if !hasDigit {
		missing = append(missing, "数字")
	}
	if !hasSpecial {
		missing = append(missing, "特殊字符")
	}
	if len(missing) > 0 {
		return fmt.Errorf("密码必须包含: %s", strings.Join(missing, "、"))
	}
	return nil
}

// ChangePassword 修改密码
func ChangePassword(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_REQUEST", "message": "请求参数错误"},
		})
		return
	}

	if !user.HasPassword() {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "UNSUPPORTED", "message": "此账号不支持密码登录"},
		})
		return
	}

	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.OldPassword)) != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_PASSWORD", "message": "原始密码错误"},
		})
		return
	}

	if err := validatePasswordComplexity(req.NewPassword); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "WEAK_PASSWORD", "message": err.Error()},
		})
		return
	}

	if req.OldPassword == req.NewPassword {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "SAME_PASSWORD", "message": "新密码不能与原始密码相同"},
		})
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "密码加密失败"},
		})
		return
	}

	user.PasswordHash = string(newHash)
	user.ResetLoginFail()
	user.UnlockLogin()
	if err := database.DB.Save(user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "保存密码失败"},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "密码修改成功"})
}

// GetAPIKeyIPWhitelist 获取API Key IP白名单
func GetAPIKeyIPWhitelist(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"ip_whitelist": user.GetAPIKeyIPWhitelist()},
	})
}

// UpdateAPIKeyIPWhitelistRequest 更新IP白名单请求
type UpdateAPIKeyIPWhitelistRequest struct {
	IPWhitelist []string `json:"ip_whitelist"`
}

// UpdateAPIKeyIPWhitelist 更新API Key IP白名单
func UpdateAPIKeyIPWhitelist(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var req UpdateAPIKeyIPWhitelistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_REQUEST", "message": "请求参数错误"},
		})
		return
	}

	var cleaned []string
	for _, ip := range req.IPWhitelist {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		if !isValidIPOrCIDR(ip) {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "INVALID_IP",
					"message": fmt.Sprintf("无效的IP地址格式: %s", ip),
				},
			})
			return
		}
		cleaned = append(cleaned, ip)
	}

	if len(cleaned) > 50 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "TOO_MANY_IPS", "message": "IP白名单最多支持50个"},
		})
		return
	}

	if err := user.SetAPIKeyIPWhitelist(cleaned); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "保存IP白名单失败"},
		})
		return
	}

	if err := database.DB.Save(user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "保存失败"},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"ip_whitelist": user.GetAPIKeyIPWhitelist()},
	})
}

// isValidIPOrCIDR 验证IP或CIDR格式
func isValidIPOrCIDR(ip string) bool {
	if strings.Contains(ip, "/") {
		_, _, err := net.ParseCIDR(ip)
		return err == nil
	}
	return net.ParseIP(ip) != nil
}

// webUser 返回给模板的用户视图，避免把敏感字段渲染进页面
type userView struct {
	ID       uint
	Username string
	APIKey   string
	IsAdmin  bool
	Role     string
	UserType string
}

func webUser(u *models.User) *userView {
	if u == nil {
		return nil
	}
	return &userView{
		ID:       u.ID,
		Username: u.Username,
		APIKey:   u.APIKey,
		IsAdmin:  u.IsAdministrator(),
		Role:     u.Role,
		UserType: u.UserType,
	}
}
