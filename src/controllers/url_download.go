package controllers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"r2proxy/src/config"
	"r2proxy/src/database"
	"r2proxy/src/middleware"
	"r2proxy/src/models"
	"r2proxy/src/services"
	"strings"

	"github.com/gin-gonic/gin"
)

// URLDownloadRequest URL下载请求
type URLDownloadRequest struct {
	URL      string `json:"url" binding:"required,url,max=2048"`
	Filename string `json:"filename" binding:"max=256"`
	Remark   string `json:"remark" binding:"max=256"`
}

// createDownloadTask 创建并启动下载任务
func createDownloadTask(userID uint, req URLDownloadRequest, tempTokenID, shareTokenID *uint) (*models.DownloadTask, error) {
	task := models.DownloadTask{
		UserID:       userID,
		URL:          req.URL,
		CustomName:   strings.TrimSpace(req.Filename),
		Remark:       strings.TrimSpace(req.Remark),
		Status:       models.TaskStatusPending,
		TempTokenID:  tempTokenID,
		ShareTokenID: shareTokenID,
		Size:         -1,
	}

	if err := database.DB.Create(&task).Error; err != nil {
		return nil, err
	}

	services.SubmitTask(task.ID)
	return &task, nil
}

// URLDownload 创建后台下载任务
func URLDownload(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var req URLDownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_REQUEST", "message": "请求参数错误，请检查 URL"},
		})
		return
	}

	task, err := createDownloadTask(user.ID, req, nil, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "创建任务失败"},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"task":    task,
			"task_id": task.ID,
			"message": "任务已创建，正在后台下载",
		},
	})
}

// taskView 下载任务对外视图：长 URL 只保留短写法，避免前端把列撑开
type taskView struct {
	models.DownloadTask
	URL      string `json:"URL"`
	ShortURL string `json:"ShortURL"`
}

func toTaskViews(tasks []models.DownloadTask) []taskView {
	out := make([]taskView, 0, len(tasks))
	for _, t := range tasks {
		v := taskView{DownloadTask: t, ShortURL: t.ShortURL()}
		// 前端展示用短地址即可，完整地址不再下发，防止长链接占满列表
		v.DownloadTask.URL = t.ShortURL()
		out = append(out, v)
	}
	return out
}

// ListDownloadTasks 列出下载任务
func ListDownloadTasks(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var tasks []models.DownloadTask
	database.DB.Where("user_id = ?", user.ID).Order("created_at DESC").Limit(50).Find(&tasks)

	c.JSON(http.StatusOK, gin.H{"success": true, "data": toTaskViews(tasks)})
}

// GetDownloadTask 获取下载任务状态
func GetDownloadTask(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var task models.DownloadTask
	if err := database.DB.Where("id = ? AND user_id = ?", c.Param("id"), user.ID).First(&task).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"code": "NOT_FOUND", "message": "任务不存在"},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"task":     task,
			"progress": task.Progress(),
		},
	})
}

// CancelDownloadTask 取消任务
func CancelDownloadTask(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var task models.DownloadTask
	if err := database.DB.Where("id = ? AND user_id = ?", c.Param("id"), user.ID).First(&task).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"code": "NOT_FOUND", "message": "任务不存在"},
		})
		return
	}

	services.CancelTask(task.ID)
	task.Status = models.TaskStatusCancelled
	task.Error = "任务已取消"
	database.DB.Save(&task)

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "任务已取消"})
}

// DeleteDownloadTask 删除下载任务
func DeleteDownloadTask(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var task models.DownloadTask
	if err := database.DB.Where("id = ? AND user_id = ?", c.Param("id"), user.ID).First(&task).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"code": "NOT_FOUND", "message": "任务不存在"},
		})
		return
	}

	if services.IsRunning(task.ID) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "TASK_RUNNING", "message": "任务正在执行中，请先取消"},
		})
		return
	}

	database.DB.Delete(&task)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "任务已删除"})
}

// ClearDownloadTasks 清空下载任务（保留正在执行的任务）
func ClearDownloadTasks(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	result := database.DB.
		Where("user_id = ? AND status NOT IN ?", user.ID, []string{models.TaskStatusDownloading, models.TaskStatusPending}).
		Delete(&models.DownloadTask{})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"deleted": result.RowsAffected},
		"message": "已清空任务",
	})
}

// ===== 分享令牌（Sign PUT 链接）=====

// findValidShareToken 查找有效分享令牌
func findValidShareToken(c *gin.Context, tokenStr string) (*models.ShareToken, bool) {
	var token models.ShareToken
	if err := database.DB.Where("token = ?", tokenStr).First(&token).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_TOKEN", "message": "分享链接无效"},
		})
		return nil, false
	}
	if token.Revoked {
		c.JSON(http.StatusGone, gin.H{
			"success": false,
			"error":   gin.H{"code": "TOKEN_REVOKED", "message": "分享链接已被撤销"},
		})
		return nil, false
	}
	if token.IsExpired() {
		c.JSON(http.StatusGone, gin.H{
			"success": false,
			"error":   gin.H{"code": "TOKEN_EXPIRED", "message": "分享链接已过期"},
		})
		return nil, false
	}
	return &token, true
}

// TempURLDownload 临时授权创建后台下载任务
func TempURLDownload(c *gin.Context) {
	token, ok := findValidTempToken(c, c.Param("token"))
	if !ok {
		return
	}

	var req URLDownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_REQUEST", "message": "请求参数错误"},
		})
		return
	}

	task, err := createDownloadTask(token.UserID, req, &token.ID, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "创建任务失败"},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"task": task, "task_id": task.ID, "message": "任务已创建，正在后台下载"},
	})
}

// TempListDownloadTasks 临时授权列出下载任务
func TempListDownloadTasks(c *gin.Context) {
	token, ok := findValidTempToken(c, c.Param("token"))
	if !ok {
		return
	}

	var tasks []models.DownloadTask
	database.DB.Where("temp_token_id = ?", token.ID).Order("created_at DESC").Limit(50).Find(&tasks)

	c.JSON(http.StatusOK, gin.H{"success": true, "data": toTaskViews(tasks)})
}

// TempDeleteDownloadTask 临时授权删除下载任务
func TempDeleteDownloadTask(c *gin.Context) {
	token, ok := findValidTempToken(c, c.Param("token"))
	if !ok {
		return
	}

	var task models.DownloadTask
	if err := database.DB.Where("id = ? AND temp_token_id = ?", c.Param("id"), token.ID).First(&task).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"code": "NOT_FOUND", "message": "任务不存在"},
		})
		return
	}

	if services.IsRunning(task.ID) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "TASK_RUNNING", "message": "任务正在执行中，请先取消"},
		})
		return
	}

	database.DB.Delete(&task)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "任务已删除"})
}

// TempError 统一错误提示
func downloadError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "任务已取消"
	case errors.Is(err, context.DeadlineExceeded):
		return "任务超时"
	case errors.Is(err, services.ErrTooLarge):
		return fmt.Sprintf("文件超过大小限制（%d 字节）", config.AppConfig.MaxDownloadSize)
	case errors.Is(err, services.ErrNotAllowed):
		return err.Error()
	default:
		log.Printf("下载失败: %v", err)
		return err.Error()
	}
}
