package controllers

import (
	"log"
	"net/http"
	"r2proxy/src/database"
	"r2proxy/src/middleware"
	"r2proxy/src/models"
	"r2proxy/src/services"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// CreateTempTokenRequest 创建临时授权请求
type CreateTempTokenRequest struct {
	Description string `json:"description" binding:"max=256"`
	ExpiresIn   int    `json:"expires_in"` // 分钟，默认30，最大 7 天
}

// CreateTempToken 创建临时授权令牌
func CreateTempToken(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var req CreateTempTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		req = CreateTempTokenRequest{}
	}

	expiresIn := 30
	if req.ExpiresIn > 0 && req.ExpiresIn <= 10080 {
		expiresIn = req.ExpiresIn
	}

	token := models.TemporaryToken{
		Token:       models.GenerateToken(),
		UserID:      user.ID,
		Description: strings.TrimSpace(req.Description),
		ExpiresAt:   time.Now().Add(time.Duration(expiresIn) * time.Minute),
	}

	if err := database.DB.Create(&token).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "创建令牌失败"},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"id":         token.ID,
			"token":      token.Token,
			"expires_at": token.ExpiresAt.Format(time.RFC3339),
			"expires_in": expiresIn,
			"upload_url": middleware.BasePathOf(c) + "/temp/" + token.Token,
		},
	})
}

// tokenWithCount 令牌 + 文件统计
type tokenWithCount struct {
	models.TemporaryToken
	FileCount int    `json:"file_count"`
	TotalSize int64  `json:"total_size"`
	Status    string `json:"status"`
	Remaining int64  `json:"remaining_seconds"`
}

// ListTempTokens 列出临时授权令牌
func ListTempTokens(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var tokens []models.TemporaryToken
	database.DB.Where("user_id = ?", user.ID).Order("created_at DESC").Limit(200).Find(&tokens)

	type stat struct {
		TokenID uint
		Cnt     int64
		Size    int64
	}
	var stats []stat
	ids := make([]uint, 0, len(tokens))
	for _, t := range tokens {
		ids = append(ids, t.ID)
	}
	if len(ids) > 0 {
		database.DB.Model(&models.File{}).
			Select("temporary_token_id as token_id, COUNT(*) as cnt, COALESCE(SUM(size),0) as size").
			Where("temporary_token_id IN ? AND status != ?", ids, models.FileStatusDeleted).
			Group("temporary_token_id").
			Scan(&stats)
	}
	statMap := make(map[uint]stat, len(stats))
	for _, s := range stats {
		statMap[s.TokenID] = s
	}

	result := make([]tokenWithCount, 0, len(tokens))
	for _, t := range tokens {
		s := statMap[t.ID]
		status := "active"
		if t.IsExpired() {
			status = "expired"
		}
		result = append(result, tokenWithCount{
			TemporaryToken: t,
			FileCount:      int(s.Cnt),
			TotalSize:      s.Size,
			Status:         status,
			Remaining:      int64(t.RemainingTime().Seconds()),
		})
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// DeleteTempToken 删除临时授权令牌
func DeleteTempToken(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var token models.TemporaryToken
	if err := database.DB.Where("id = ? AND user_id = ?", c.Param("id"), user.ID).First(&token).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"code": "NOT_FOUND", "message": "令牌不存在"},
		})
		return
	}

	// 关联文件保留，仅解除关联
	database.DB.Model(&models.File{}).Where("temporary_token_id = ?", token.ID).UpdateColumn("temporary_token_id", nil)
	database.DB.Delete(&token)

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "令牌已删除"})
}

// ClearTempTokens 清空临时授权令牌（保留已上传文件）
func ClearTempTokens(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var tokens []models.TemporaryToken
	database.DB.Where("user_id = ?", user.ID).Find(&tokens)

	ids := make([]uint, 0, len(tokens))
	for _, t := range tokens {
		ids = append(ids, t.ID)
	}

	if len(ids) > 0 {
		database.DB.Model(&models.File{}).Where("temporary_token_id IN ?", ids).UpdateColumn("temporary_token_id", nil)
		database.DB.Where("id IN ?", ids).Delete(&models.TemporaryToken{})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"deleted": len(ids)},
		"message": "已清空授权",
	})
}

// findValidTempToken 按 token 查找有效令牌
func findValidTempToken(c *gin.Context, tokenStr string) (*models.TemporaryToken, bool) {
	var token models.TemporaryToken
	if err := database.DB.Where("token = ?", tokenStr).First(&token).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_TOKEN", "message": "无效的授权令牌"},
		})
		return nil, false
	}
	if token.IsExpired() {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   gin.H{"code": "TOKEN_EXPIRED", "message": "授权令牌已过期"},
		})
		return nil, false
	}
	return &token, true
}

// TempUploadPage 临时上传页面
func TempUploadPage(c *gin.Context) {
	tokenStr := c.Param("token")

	var token models.TemporaryToken
	if err := database.DB.Where("token = ?", tokenStr).First(&token).Error; err != nil {
		renderError(c, http.StatusNotFound, "链接无效", "临时授权链接不存在或已失效")
		return
	}
	if token.IsExpired() {
		renderError(c, http.StatusGone, "链接已过期", "临时授权链接已过期，请联系分享者获取新链接")
		return
	}

	var files []models.File
	database.DB.Where("temporary_token_id = ? AND status != ?", token.ID, models.FileStatusDeleted).
		Order("created_at DESC").
		Limit(500).
		Find(&files)

	c.HTML(http.StatusOK, "temp_upload.html", gin.H{
		"title":       "临时上传",
		"barTitle":    "临时上传",
		"token":       token.Token,
		"expires_at":  token.ExpiresAt.Format("2006-01-02 15:04"),
		"remaining":   int64(token.RemainingTime().Seconds()),
		"description": token.Description,
		"files":       files,
		"hasFileSection": true,
		"basePath":    middleware.BasePathOf(c),
	})
}

// renderError 渲染错误页
func renderError(c *gin.Context, status int, title, message string) {
	c.Header("Cache-Control", "no-store")
	c.HTML(status, "error.html", gin.H{
		"title":    title,
		"message":  message,
		"basePath": middleware.BasePathOf(c),
	})
}

// TempSignUpload 临时授权上传签名
func TempSignUpload(c *gin.Context) {
	token, ok := findValidTempToken(c, c.Param("token"))
	if !ok {
		return
	}

	var req SignUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_REQUEST", "message": "请求参数错误"},
		})
		return
	}

	var user models.User
	if err := database.DB.First(&user, token.UserID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "授权用户不存在"},
		})
		return
	}

	remark := token.Description
	if remark == "" {
		remark = req.Filename
	}

	result, status, errResp := newPendingUpload(c, &user, "temp", req.Filename, req.ContentType, normalizeTTL(req.ExpiresIn), &token.ID, nil, remark)
	if status != http.StatusOK {
		c.JSON(status, errResp)
		return
	}

	// 仅在上传签名创建成功时计数
	database.DB.Model(&models.TemporaryToken{}).Where("id = ?", token.ID).
		Updates(map[string]any{"used_count": gorm.Expr("used_count + 1")})

	c.JSON(http.StatusOK, uploadPayload(c, result, nil))
}

// TempListFiles 临时授权文件列表
func TempListFiles(c *gin.Context) {
	token, ok := findValidTempToken(c, c.Param("token"))
	if !ok {
		return
	}

	var files []models.File
	database.DB.Where("temporary_token_id = ? AND status != ?", token.ID, models.FileStatusDeleted).
		Order("created_at DESC").
		Limit(500).
		Find(&files)

	c.JSON(http.StatusOK, gin.H{"success": true, "data": files})
}

// tempFile 查找令牌下属于自己的文件
func tempFile(c *gin.Context, token *models.TemporaryToken) (*models.File, bool) {
	var file models.File
	if err := database.DB.Where("id = ? AND temporary_token_id = ?", c.Param("id"), token.ID).First(&file).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"code": "NOT_FOUND", "message": "文件记录不存在"},
		})
		return nil, false
	}
	return &file, true
}

// TempConfirmUpload 临时授权确认上传
func TempConfirmUpload(c *gin.Context) {
	token, ok := findValidTempToken(c, c.Param("token"))
	if !ok {
		return
	}
	file, ok := tempFile(c, token)
	if !ok {
		return
	}
	if file.Status != models.FileStatusPending && file.Status != models.FileStatusFailed {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_REQUEST", "message": "文件状态不正确"},
		})
		return
	}

	status, body := confirmFileRecord(c, file)
	c.JSON(status, body)
}

// TempGetDownloadURL 获取临时授权下载链接
func TempGetDownloadURL(c *gin.Context) {
	token, ok := findValidTempToken(c, c.Param("token"))
	if !ok {
		return
	}
	file, ok := tempFile(c, token)
	if !ok {
		return
	}
	if file.Status != models.FileStatusConfirmed {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "FILE_NOT_READY", "message": "文件未就绪"},
		})
		return
	}

	downloadURL, status, body := downloadFileRecord(c, file, shareDownloadTTL)
	if status != http.StatusOK {
		c.JSON(status, body)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"download_url": downloadURL,
			"filename":     file.Filename,
			"size":         file.Size,
			"public_url":   services.R2Service.PublicURL(file.Key),
		},
	})
}

// TempDeleteFile 临时授权删除文件
func TempDeleteFile(c *gin.Context) {
	token, ok := findValidTempToken(c, c.Param("token"))
	if !ok {
		return
	}
	file, ok := tempFile(c, token)
	if !ok {
		return
	}

	if err := services.R2Service.DeleteObject(c.Request.Context(), file.Key); err != nil {
		log.Printf("临时授权删除对象失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "DELETE_FAILED", "message": "删除存储对象失败"},
		})
		return
	}
	database.DB.Delete(file)

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "文件已删除"})
}

// parseID 解析路径 ID
func parseID(raw string) uint {
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0
	}
	return uint(n)
}
