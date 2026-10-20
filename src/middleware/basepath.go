package middleware

import (
	"r2proxy/src/config"

	"github.com/gin-gonic/gin"
)

// BasePath 注入安全入口前缀，关闭时为空串
func BasePath() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("basePath", BasePathOf(c))
		c.Next()
	}
}

// BasePathOf 读取当前请求的安全入口前缀
func BasePathOf(c *gin.Context) string {
	if v, ok := c.Get("basePath"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	// 中间件未注册时的兜底
	if config.AppConfig != nil && config.AppConfig.SecurityEntry != "" {
		return "/" + config.AppConfig.SecurityEntry
	}
	return ""
}
