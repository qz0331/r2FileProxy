package middleware

import (
	"net/http"
	"net/url"
	"r2proxy/src/database"
	"r2proxy/src/models"
	"strings"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

// AuthProvider 认证来源
const (
	AuthTypeSession = "session"
	AuthTypeAPIKey  = "api_key"
)

// Auth 认证中间件
//
// 支持两种方式：
//  1. Session（Web 登录）
//  2. X-API-Key 请求头（程序调用；Web 页面同样携带 X-API-Key 也可用，
//     因此 API Key 用户可以直接在 Web 设置页获取签名 URL 上传）
func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. Session 认证
		session := sessions.Default(c)
		if uid, ok := session.Get("user_id").(uint); ok && uid > 0 {
			var user models.User
			if err := database.DB.First(&user, uid).Error; err == nil {
				c.Set("user", &user)
				c.Set("auth_type", AuthTypeSession)
				c.Next()
				return
			}
			// Session 指向的用户已不存在，清空以便前端重新登录
			session.Clear()
			_ = session.Save()
		}

		// 2. API Key 认证
		apiKey := strings.TrimSpace(c.GetHeader("X-API-Key"))
		if apiKey != "" && len(apiKey) >= 16 {
			var user models.User
			if err := database.DB.
				Where("api_key = ? AND user_type IN ?", apiKey, []string{models.UserTypeAPI, models.UserTypeBoth}).
				First(&user).Error; err == nil {
				if !user.IsIPAllowed(c.ClientIP()) {
					c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
						"success": false,
						"error": gin.H{
							"code":    "IP_NOT_ALLOWED",
							"message": "IP地址不在白名单中",
						},
					})
					return
				}
				c.Set("user", &user)
				c.Set("auth_type", AuthTypeAPIKey)
				c.Next()
				return
			}
		}

		// 3. 认证失败
		if isAPIPath(c.Request.URL.Path) ||
			strings.Contains(c.GetHeader("Accept"), "application/json") ||
			c.GetHeader("X-Requested-With") == "XMLHttpRequest" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "UNAUTHORIZED",
					"message": "未授权访问",
				},
			})
			return
		}

		redirectToLogin(c)
	}
}

// redirectToLogin 重定向到登录页，并带上原始地址
func redirectToLogin(c *gin.Context) {
	base := BasePathOf(c)
	target := base + "/login"
	if c.Request.Method == http.MethodGet {
		if rawURI := c.Request.URL.RequestURI(); rawURI != "" && rawURI != "/" {
			target += "?next=" + url.QueryEscape(rawURI)
		}
	}
	c.Redirect(http.StatusFound, target)
	c.Abort()
}

// AdminAuth 管理员认证中间件
func AdminAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := GetCurrentUser(c)
		if user == nil || !user.IsAdministrator() {
			if isAPIPath(c.Request.URL.Path) {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"success": false,
					"error": gin.H{
						"code":    "FORBIDDEN",
						"message": "需要管理员权限",
					},
				})
				return
			}
			c.Redirect(http.StatusFound, BasePathOf(c)+"/")
			c.Abort()
			return
		}
		c.Next()
	}
}

// isAPIPath 判断请求是否指向 API（兼容安全入口前缀）
func isAPIPath(path string) bool {
	return strings.Contains(path, "/api/") || strings.HasSuffix(path, "/api")
}

// GetCurrentUser 获取当前用户
func GetCurrentUser(c *gin.Context) *models.User {
	if user, exists := c.Get("user"); exists {
		if u, ok := user.(*models.User); ok {
			return u
		}
	}
	return nil
}

// IsSessionAuth 当前请求是否来自 Web 登录
func IsSessionAuth(c *gin.Context) bool {
	return c.GetString("auth_type") == AuthTypeSession
}
