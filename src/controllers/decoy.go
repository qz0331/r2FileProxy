package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// DecoyPage 未携带安全入口时的伪装页（延迟检测）
func DecoyPage(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.HTML(http.StatusOK, "decoy.html", gin.H{})
}
