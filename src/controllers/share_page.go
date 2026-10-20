package controllers

import (
	"net/http"
	"r2proxy/src/database"
	"r2proxy/src/middleware"
	"r2proxy/src/models"
	"time"

	"github.com/gin-gonic/gin"
)

// SharesPage 设备分享管理页面
func SharesPage(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var shares []models.ShareToken
	database.DB.Where("user_id = ?", user.ID).Order("created_at DESC").Limit(100).Find(&shares)

	var pendingLinks []models.File
	database.DB.Where("user_id = ? AND status = ?", user.ID, models.FileStatusPending).
		Order("created_at DESC").Limit(20).Find(&pendingLinks)

	type pendingRow struct {
		ID         uint
		Filename   string
		UploaderIP string
		CreatedAt  time.Time
		Expired    bool
	}
	rows := make([]pendingRow, 0, len(pendingLinks))
	for _, f := range pendingLinks {
		rows = append(rows, pendingRow{
			ID:         f.ID,
			Filename:   f.Filename,
			UploaderIP: f.UploaderIP,
			CreatedAt:  f.CreatedAt,
			Expired:    f.UploadExpiresAt != nil && time.Now().After(*f.UploadExpiresAt),
		})
	}

	c.HTML(http.StatusOK, "shares.html", gin.H{
		"title":        "设备分享",
		"user":         webUser(user),
		"active":       "shares",
		"shares":       shares,
		"pendingLinks": rows,
		"basePath":     middleware.BasePathOf(c),
	})
}
