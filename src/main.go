package main

import (
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"r2proxy/src/config"
	"r2proxy/src/controllers"
	"r2proxy/src/database"
	"r2proxy/src/middleware"
	"r2proxy/src/models"
	"r2proxy/src/routes"
	"r2proxy/src/scripts"
	"r2proxy/src/services"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// viewsGlob 定位模板目录，兼容仓库运行与发布包运行
func viewsGlob() string {
	for _, dir := range []string{"src/views", "views", "./views"} {
		if info, err := os.Stat(filepath.Join(dir, "index.html")); err == nil && !info.IsDir() {
			return filepath.Join(dir, "*")
		}
	}
	return "src/views/*"
}

// humanTimeAgo 返回“刚刚 / x 分钟前 / x 天前”等相对时间
func humanTimeAgo(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	diff := time.Since(t)
	switch {
	case diff < time.Minute:
		return "刚刚"
	case diff < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(diff.Minutes()))
	case diff < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(diff.Hours()))
	case diff < 30*24*time.Hour:
		return fmt.Sprintf("%d 天前", int(diff.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

// firstRune 返回首个字符，用于头像占位
func firstRune(s string) string {
	for _, r := range strings.TrimSpace(s) {
		return strings.ToUpper(string(r))
	}
	return "?"
}

func main() {
	if os.Getenv("GIN_MODE") == "" {
		os.Setenv("GIN_MODE", "release")
	}

	config.Load()

	if err := database.Init(config.AppConfig.DatabaseURL); err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	defer database.Close()

	if err := scripts.InitAdmin(); err != nil {
		log.Printf("警告: 管理员初始化失败: %v", err)
	}

	if err := services.InitR2(); err != nil {
		log.Fatalf("R2 初始化失败: %v", err)
	}

	gin.SetMode(os.Getenv("GIN_MODE"))
	r := gin.Default()

	// 反向代理信任配置：默认不信任任何代理头，避免伪造 X-Forwarded-For 绕过 IP 白名单
	if len(config.AppConfig.TrustedProxies) > 0 {
		if err := r.SetTrustedProxies(config.AppConfig.TrustedProxies); err != nil {
			log.Fatalf("TRUSTED_PROXIES 配置无效: %v", err)
		}
		log.Printf("已信任代理: %v", config.AppConfig.TrustedProxies)
	} else {
		_ = r.SetTrustedProxies(nil)
	}

	// Session 中间件
	store := cookie.NewStore([]byte(config.AppConfig.ServerSecret))
	store.Options(sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7,
		HttpOnly: true,
		Secure:   config.AppConfig.SecureCookie,
		SameSite: 2, // http.SameSiteLaxMode
	})
	r.Use(sessions.Sessions("r2proxy_session", store))

	// 安全响应头 + 缓存策略
	r.Use(func(c *gin.Context) {
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "same-origin")
		c.Header("X-XSS-Protection", "1; mode=block")
		if strings.Contains(c.Request.URL.Path, "/static/") {
			// 静态资源带 ?v=hash，可长期缓存
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			c.Header("Cache-Control", "no-store")
		}
		c.Next()
	})

	// 安全入口前缀注入
	r.Use(middleware.BasePath())

	r.SetFuncMap(template.FuncMap{
		"add":        func(a, b int) int { return a + b },
		"sub":        func(a, b int) int { return a - b },
		"formatSize": models.FormatBytes,
		"formatTime": func(t time.Time) string { return t.Format("2006-01-02 15:04") },
		"timeAgo":    humanTimeAgo,
		"lower":      strings.ToLower,
		"initial":    firstRune,
	})

	r.LoadHTMLGlob(viewsGlob())

	routes.Setup(r)

	// 启用安全入口时，未匹配的路径一律返回伪装页
	if config.AppConfig.SecurityEntry != "" {
		r.NoRoute(controllers.DecoyPage)
	}

	log.Printf("服务启动在 :%s", config.AppConfig.ServerPort)
	if err := r.Run(":" + config.AppConfig.ServerPort); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}
