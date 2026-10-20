package routes

import (
	"net/http"
	"os"
	"path/filepath"
	"r2proxy/src/config"
	"r2proxy/src/controllers"
	"r2proxy/src/middleware"

	"github.com/gin-gonic/gin"
)

// staticDir 定位静态资源目录，兼容从仓库根目录与发布目录启动
func staticDir() string {
	for _, dir := range []string{"./src/static", "./static"} {
		if info, err := os.Stat(filepath.Join(dir, "app.css")); err == nil && !info.IsDir() {
			return dir
		}
	}
	return "./src/static"
}

// Setup 设置路由
func Setup(r *gin.Engine) {
	// 安全入口前缀（关闭时为空串，即根路径）
	base := ""
	if config.AppConfig.SecurityEntry != "" {
		base = "/" + config.AppConfig.SecurityEntry
	}

	// 页面与临时授权路由（受安全入口保护）
	page := r.Group(base)
	page.Static("/static", staticDir())

	// 健康检查：根路径与安全入口下都可用，便于容器探针与监控
	healthz := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true, "status": "ok"})
	}
	r.GET("/healthz", healthz)
	if base != "" {
		page.GET("/healthz", healthz)
	}

	// 公开页面路由
	page.GET("/login", controllers.LoginPage)

	// 认证 API（公开）
	page.POST("/login", controllers.Login)
	page.POST("/logout", controllers.Logout)

	// 临时授权页面（公开）
	page.GET("/temp/:token", controllers.TempUploadPage)
	page.POST("/temp/:token/sign/upload", controllers.TempSignUpload)
	page.POST("/temp/:token/upload", controllers.TempUploadProxy)
	page.GET("/temp/:token/files", controllers.TempListFiles)
	page.POST("/temp/:token/files/:id/confirm", controllers.TempConfirmUpload)
	page.GET("/temp/:token/files/:id/download-url", controllers.TempGetDownloadURL)
	page.DELETE("/temp/:token/files/:id", controllers.TempDeleteFile)
	page.POST("/temp/:token/download-tasks", controllers.TempURLDownload)
	page.GET("/temp/:token/download-tasks", controllers.TempListDownloadTasks)
	page.DELETE("/temp/:token/download-tasks/:id", controllers.TempDeleteDownloadTask)

	// 通用 PUT 上传页（把 Sign PUT 链接变成可点选文件的页面，方便手机使用）
	page.GET("/put", controllers.PutPage)
	page.POST("/put/upload", controllers.PutProxy)
	page.GET("/put/info", controllers.PutInfo)

	// 设备分享页面（Sign PUT 链接，公开访问）
	page.GET("/s/:token", controllers.SharePage)
	page.GET("/s/:token/info", controllers.ShareInfo)
	page.POST("/s/:token/sign/upload", controllers.ShareSignUpload)
	page.POST("/s/:token/upload", controllers.ShareUpload)
	page.GET("/s/:token/files", controllers.ShareListFiles)
	page.POST("/s/:token/files/:id/confirm", controllers.ShareConfirm)
	page.GET("/s/:token/files/:id/download-url", controllers.ShareDownload)
	page.DELETE("/s/:token/files/:id", controllers.ShareDeleteFile)
	page.POST("/s/:token/download-tasks", controllers.ShareURLDownload)
	page.GET("/s/:token/download-tasks", controllers.ShareListTasks)
	page.DELETE("/s/:token/download-tasks/:id", controllers.ShareDeleteTask)

	// 需要认证的页面
	authed := r.Group(base, middleware.Auth())
	authed.GET("/", controllers.Index)
	authed.GET("/files", controllers.FilesPage)
	authed.GET("/settings", controllers.SettingsPage)
	authed.GET("/shares", controllers.SharesPage)
	authed.GET("/users", middleware.AdminAuth(), controllers.UserPage)

	// API 路由（需要认证）。同时挂载在根路径与安全入口下，
	// 这样无论前端用 /api 还是 {basePath}/api 都能访问。
	registerAPI(r.Group("/api", middleware.Auth()))
	if base != "" {
		registerAPI(r.Group(base+"/api", middleware.Auth()))
	}
}

// registerAPI 注册所有需要认证的 API
func registerAPI(api *gin.RouterGroup) {
	// API Key 管理
	api.GET("/key", controllers.GetAPIKey)
	api.POST("/key/regenerate", controllers.RegenerateAPIKey)
	api.GET("/key/ip-whitelist", controllers.GetAPIKeyIPWhitelist)
	api.PUT("/key/ip-whitelist", controllers.UpdateAPIKeyIPWhitelist)

	// 密码管理
	api.POST("/password/change", controllers.ChangePassword)

	// 签名 URL
	api.POST("/sign/upload", controllers.SignUpload)
	api.POST("/sign/download", controllers.SignDownload)

	// Sign PUT 链接（给其他设备直接上传）
	api.POST("/sign/put", controllers.SignPutLink)
	api.GET("/sign/put", controllers.ListPutLinks)
	api.POST("/sign/put/:id/regenerate", controllers.RegeneratePutLink)

	// 代理上传（不依赖 R2 CORS，支持大文件分片与自动压缩）
	api.POST("/upload", controllers.AuthUpload)

	// 临时授权管理
	api.POST("/temp-tokens", controllers.CreateTempToken)
	api.GET("/temp-tokens", controllers.ListTempTokens)
	api.DELETE("/temp-tokens", controllers.ClearTempTokens)
	api.DELETE("/temp-tokens/:id", controllers.DeleteTempToken)

	// 设备分享（Sign PUT 链接）管理
	api.POST("/shares", controllers.CreateShare)
	api.GET("/shares", controllers.ListShares)
	api.PUT("/shares/:id", controllers.UpdateShare)
	api.DELETE("/shares/:id", controllers.DeleteShare)

	// 文件管理
	api.GET("/files", controllers.ListFiles)
	api.GET("/files/:id/download-url", controllers.GetDownloadURL)
	api.GET("/files/:id/download", controllers.ProxyDownload)
	api.POST("/files/:id/confirm", controllers.ConfirmUpload)
	api.PUT("/files/:id/remark", controllers.UpdateRemark)
	api.DELETE("/files/:id", controllers.DeleteFile)
	api.POST("/files/batch-delete", controllers.BatchDeleteFile)

	// URL下载任务
	api.POST("/download-tasks", controllers.URLDownload)
	api.GET("/download-tasks", controllers.ListDownloadTasks)
	api.GET("/download-tasks/:id", controllers.GetDownloadTask)
	api.POST("/download-tasks/:id/cancel", controllers.CancelDownloadTask)
	api.DELETE("/download-tasks", controllers.ClearDownloadTasks)
	api.DELETE("/download-tasks/:id", controllers.DeleteDownloadTask)

	// 用户管理（管理员）
	api.POST("/users", middleware.AdminAuth(), controllers.CreateUser)
	api.GET("/users", middleware.AdminAuth(), controllers.ListUsers)
	api.DELETE("/users/:id", middleware.AdminAuth(), controllers.DeleteUser)
}
