package controllers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"r2proxy/src/config"
	"r2proxy/src/database"
	"r2proxy/src/middleware"
	"r2proxy/src/models"
	"r2proxy/src/services"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const deviceCookieName = "r2p_device"

// CreateShareRequest 创建分享令牌请求
type CreateShareRequest struct {
	Name         string `json:"name" binding:"max=64"`
	Description  string `json:"description" binding:"max=256"`
	Mode         string `json:"mode" binding:"omitempty,oneof=upload both"`
	Prefix       string `json:"prefix" binding:"max=128"`
	ExpiresIn    int    `json:"expires_in"`     // 分钟，默认 1440（24 小时）
	MaxUploads   int    `json:"max_uploads"`    // 0 = 不限制
	MaxTotalSize int64  `json:"max_total_size"` // 字节，0 = 不限制
	MaxFileSize  int64  `json:"max_file_size"`  // 字节，0 = 不限制
}

// CreateShare 创建分享（Sign PUT）令牌
func CreateShare(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var req CreateShareRequest
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_REQUEST", "message": "请求参数错误"},
		})
		return
	}

	expiresIn := 1440 // 默认 24 小时
	if req.ExpiresIn > 0 && req.ExpiresIn <= 10080 {
		expiresIn = req.ExpiresIn
	}

	mode := req.Mode
	if mode != models.ShareModeBoth {
		mode = models.ShareModeUpload
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "设备分享 " + time.Now().Format("01-02 15:04")
	}

	share := models.ShareToken{
		Token:        models.GenerateShareToken(),
		UserID:       user.ID,
		Name:         truncateRunes(name, 64),
		Description:  truncateRunes(strings.TrimSpace(req.Description), 256),
		Mode:         mode,
		Prefix:       sanitizePrefix(req.Prefix),
		MaxUploads:   maxInt(req.MaxUploads, 0),
		MaxTotalSize: maxInt64(req.MaxTotalSize, 0),
		MaxFileSize:  maxInt64(req.MaxFileSize, 0),
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Minute),
	}

	if err := database.DB.Create(&share).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "创建分享失败"},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": sharePayload(c, &share)})
}

// sharePayload 分享响应体
func sharePayload(c *gin.Context, share *models.ShareToken) gin.H {
	url := shareURL(c, share.Token)
	return gin.H{
		"id":             share.ID,
		"token":          share.Token,
		"name":           share.Name,
		"description":    share.Description,
		"mode":           share.Mode,
		"prefix":         share.Prefix,
		"max_uploads":    share.MaxUploads,
		"max_total_size": share.MaxTotalSize,
		"max_file_size":  share.MaxFileSize,
		"used_count":     share.UsedCount,
		"used_size":      share.UsedSize,
		"expires_at":     share.ExpiresAt.Format(time.RFC3339),
		"status":         share.Status(),
		"share_url":      url,
		"curl": fmt.Sprintf(
			"curl -X POST '%s/upload?filename=<文件名>' --upload-file '<本地文件>'",
			url,
		),
	}
}

// shareURL 生成完整分享地址
func shareURL(c *gin.Context, token string) string {
	base := config.AppConfig.PublicBaseURL
	if base == "" {
		scheme := "http"
		if c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		base = scheme + "://" + c.Request.Host
	}
	return base + middleware.BasePathOf(c) + "/s/" + token
}

// ListShares 列出分享令牌
func ListShares(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var shares []models.ShareToken
	database.DB.Where("user_id = ?", user.ID).Order("created_at DESC").Limit(100).Find(&shares)

	result := make([]gin.H, 0, len(shares))
	for i := range shares {
		result = append(result, sharePayload(c, &shares[i]))
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// UpdateShareRequest 更新分享请求
type UpdateShareRequest struct {
	Name         *string `json:"name"`
	Description  *string `json:"description"`
	Mode         *string `json:"mode"`
	ExpiresIn    *int    `json:"expires_in"` // 分钟（从当前时间重新计算）
	MaxUploads   *int    `json:"max_uploads"`
	MaxTotalSize *int64  `json:"max_total_size"`
	MaxFileSize  *int64  `json:"max_file_size"`
	Revoked      *bool   `json:"revoked"`
}

// UpdateShare 更新 / 撤销分享
func UpdateShare(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var share models.ShareToken
	if err := database.DB.Where("id = ? AND user_id = ?", c.Param("id"), user.ID).First(&share).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"code": "NOT_FOUND", "message": "分享不存在"},
		})
		return
	}

	var req UpdateShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_REQUEST", "message": "请求参数错误"},
		})
		return
	}

	updates := map[string]any{}
	if req.Name != nil {
		updates["name"] = truncateRunes(strings.TrimSpace(*req.Name), 64)
	}
	if req.Description != nil {
		updates["description"] = truncateRunes(strings.TrimSpace(*req.Description), 256)
	}
	if req.Mode != nil && (*req.Mode == models.ShareModeUpload || *req.Mode == models.ShareModeBoth) {
		updates["mode"] = *req.Mode
	}
	if req.ExpiresIn != nil {
		minutes := *req.ExpiresIn
		if minutes < 1 {
			minutes = 1
		}
		if minutes > 10080 {
			minutes = 10080
		}
		updates["expires_at"] = time.Now().Add(time.Duration(minutes) * time.Minute)
	}
	if req.MaxUploads != nil {
		updates["max_uploads"] = maxInt(*req.MaxUploads, 0)
	}
	if req.MaxTotalSize != nil {
		updates["max_total_size"] = maxInt64(*req.MaxTotalSize, 0)
	}
	if req.MaxFileSize != nil {
		updates["max_file_size"] = maxInt64(*req.MaxFileSize, 0)
	}
	if req.Revoked != nil {
		updates["revoked"] = *req.Revoked
	}

	if len(updates) > 0 {
		database.DB.Model(&share).Updates(updates)
		database.DB.First(&share, share.ID)
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": sharePayload(c, &share)})
}

// DeleteShare 删除分享
func DeleteShare(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var share models.ShareToken
	if err := database.DB.Where("id = ? AND user_id = ?", c.Param("id"), user.ID).First(&share).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"code": "NOT_FOUND", "message": "分享不存在"},
		})
		return
	}

	database.DB.Model(&models.File{}).Where("share_token_id = ?", share.ID).UpdateColumn("share_token_id", nil)
	database.DB.Delete(&share)

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "分享已删除（已上传文件保留）"})
}

// SharePage 分享上传页面（可给其他设备使用）
func SharePage(c *gin.Context) {
	ensureDeviceCookie(c)

	var share models.ShareToken
	if err := database.DB.Where("token = ?", c.Param("token")).First(&share).Error; err != nil {
		renderError(c, http.StatusNotFound, "链接无效", "分享链接不存在或已被删除")
		return
	}
	if share.Revoked {
		renderError(c, http.StatusGone, "链接已撤销", "分享链接已被创建者撤销")
		return
	}
	if share.IsExpired() {
		renderError(c, http.StatusGone, "链接已过期", "分享链接已过期，请联系分享者获取新链接")
		return
	}

	var files []models.File
	if share.Mode == models.ShareModeBoth {
		database.DB.Where("share_token_id = ? AND status != ?", share.ID, models.FileStatusDeleted).
			Order("created_at DESC").Limit(300).Find(&files)
	} else {
		database.DB.Where("share_token_id = ? AND uploader_ip = ? AND status != ?", share.ID, c.ClientIP(), models.FileStatusDeleted).
			Order("created_at DESC").Limit(300).Find(&files)
	}

	var owner models.User
	database.DB.Select("username").First(&owner, share.UserID)

	c.HTML(http.StatusOK, "share.html", gin.H{
		"title":         share.Name,
		"share":         &share,
		"token":         share.Token,
		"mode":          share.Mode,
		"owner":         owner.Username,
		"expires_at":    share.ExpiresAt.Format("2006-01-02 15:04"),
		"remaining":     int64(time.Until(share.ExpiresAt).Seconds()),
		"files":         files,
		"basePath":      middleware.BasePathOf(c),
		"maxFileSize":   effectiveMaxFileSize(&share),
		"maxTotalSize":  share.MaxTotalSize,
		"remainingCnt":  share.RemainingUploads(),
		"remainingSize": share.RemainingSize(),
		"deviceID":      deviceID(c),
		"clientIP":      c.ClientIP(),
		"hasFileSection": true,
	})
}

// ShareInfo 分享信息（供前端初始化）
func ShareInfo(c *gin.Context) {
	share, ok := findValidShareToken(c, c.Param("token"))
	if !ok {
		return
	}

	var owner models.User
	database.DB.Select("username").First(&owner, share.UserID)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"name":           share.Name,
			"description":    share.Description,
			"mode":           share.Mode,
			"owner":          owner.Username,
			"expires_at":     share.ExpiresAt.Format(time.RFC3339),
			"remaining":      int64(time.Until(share.ExpiresAt).Seconds()),
			"remaining_cnt":  share.RemainingUploads(),
			"remaining_size": share.RemainingSize(),
			"max_file_size":  effectiveMaxFileSize(share),
			"used_count":     share.UsedCount,
			"used_size":      share.UsedSize,
			"status":         share.Status(),
		},
	})
}

// effectiveMaxFileSize 单文件允许的最大字节数
func effectiveMaxFileSize(share *models.ShareToken) int64 {
	limit := share.MaxFileSize
	global := int64(0)
	if config.AppConfig != nil {
		global = config.AppConfig.MaxUploadSize
	}
	if global > 0 && (limit <= 0 || global < limit) {
		limit = global
	}
	if remain := share.RemainingSize(); remain >= 0 && (limit <= 0 || remain < limit) {
		limit = remain
	}
	return limit
}

// ShareSignUpload 生成上传签名（PUT 到 R2）
func ShareSignUpload(c *gin.Context) {
	share, ok := findValidShareToken(c, c.Param("token"))
	if !ok {
		return
	}
	if !share.CanUpload() {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"error":   gin.H{"code": "QUOTA_EXCEEDED", "message": "上传次数或容量已达上限"},
		})
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

	if err := validateFilename(req.Filename); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_FILENAME", "message": err.Error()},
		})
		return
	}

	var owner models.User
	if err := database.DB.First(&owner, share.UserID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "分享者账号不存在"},
		})
		return
	}

	remark := share.Description
	if remark == "" {
		remark = req.Filename
	}

	prefix := share.Prefix
	if prefix == "" {
		prefix = "share"
	}

	result, status, errResp := newPendingUpload(c, &owner, prefix, req.Filename, req.ContentType, normalizeTTL(req.ExpiresIn), nil, &share.ID, remark)
	if status != http.StatusOK {
		c.JSON(status, errResp)
		return
	}

	database.DB.Model(&models.ShareToken{}).Where("id = ?", share.ID).Updates(map[string]any{
		"used_count":      gorm.Expr("used_count + 1"),
		"last_used_at":    time.Now(),
		"last_ip":         c.ClientIP(),
		"last_user_agent": truncateRunes(c.Request.UserAgent(), 256),
	})

	c.JSON(http.StatusOK, uploadPayload(c, result, gin.H{
		"share_token": share.Token,
	}))
}

// ShareUpload 代理上传（浏览器不依赖 R2 CORS，支持大文件与自动压缩）
func ShareUpload(c *gin.Context) {
	share, ok := findValidShareToken(c, c.Param("token"))
	if !ok {
		return
	}
	if !share.CanUpload() {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"error":   gin.H{"code": "QUOTA_EXCEEDED", "message": "上传次数或容量已达上限"},
		})
		return
	}

	// 单文件限制：取分享限制 / 全局限制 / 剩余配额的最小值
	maxSize := effectiveMaxFileSize(share)
	if c.Request.ContentLength > 0 && maxSize > 0 && c.Request.ContentLength > maxSize {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "FILE_TOO_LARGE",
				"message": fmt.Sprintf("文件超过大小限制（最大 %s）", models.FormatBytes(maxSize)),
			},
		})
		return
	}

	var owner models.User
	if err := database.DB.First(&owner, share.UserID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "分享者账号不存在"},
		})
		return
	}

	filename := strings.TrimSpace(c.Query("filename"))
	if filename == "" {
		filename = strings.TrimSpace(c.GetHeader("X-Filename"))
	}
	if filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "MISSING_FILENAME", "message": "缺少文件名参数"},
		})
		return
	}
	if err := validateFilename(filename); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_FILENAME", "message": err.Error()},
		})
		return
	}

	contentType := c.GetHeader("X-Content-Type")
	if contentType == "" {
		contentType = c.ContentType()
	}
	if contentType == "" || len(contentType) > 128 {
		contentType = "application/octet-stream"
	}

	prefix := share.Prefix
	if prefix == "" {
		prefix = "share"
	}
	key := generateR2Key(prefix, owner.ID, filename)

	streamResult, err := services.R2Service.PutObjectStream(
		c.Request.Context(),
		key,
		c.Request.Body,
		contentType,
		shouldCompressUpload(contentType, filename),
		maxSize,
		nil,
	)
	if err != nil {
		log.Printf("分享上传失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "UPLOAD_FAILED", "message": uploadErrorMessage(err, maxSize)},
		})
		return
	}

	remark := share.Description
	if remark == "" {
		remark = filename
	}

	file := models.File{
		UserID:          owner.ID,
		ShareTokenID:    &share.ID,
		Key:             key,
		Filename:        filename,
		Remark:          truncateRunes(remark, 256),
		Size:            streamResult.RawSize,
		StoredSize:      streamResult.StoredSize,
		ContentType:     contentType,
		ContentEncoding: streamResult.ContentEncoding,
		ETag:            streamResult.ETag,
		Status:          models.FileStatusConfirmed,
		UploaderIP:      c.ClientIP(),
	}
	if err := database.DB.Create(&file).Error; err != nil {
		_ = services.R2Service.DeleteObject(c.Request.Context(), key)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "保存文件记录失败"},
		})
		return
	}

	database.DB.Model(&models.ShareToken{}).Where("id = ?", share.ID).Updates(map[string]any{
		"used_count":      gorm.Expr("used_count + 1"),
		"used_size":       gorm.Expr("used_size + ?", streamResult.RawSize),
		"last_used_at":    time.Now(),
		"last_ip":         c.ClientIP(),
		"last_user_agent": truncateRunes(c.Request.UserAgent(), 256),
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"file": gin.H{
				"id":               file.ID,
				"filename":         file.Filename,
				"size":             file.Size,
				"stored_size":      file.StoredSize,
				"content_type":     file.ContentType,
				"content_encoding": file.ContentEncoding,
				"status":           file.Status,
				"created_at":       file.CreatedAt.Format(time.RFC3339),
			},
			"compressed": streamResult.Compressed,
		},
	})
}

// ShareListFiles 分享内文件列表
func ShareListFiles(c *gin.Context) {
	share, ok := findValidShareToken(c, c.Param("token"))
	if !ok {
		return
	}

	query := database.DB.Model(&models.File{}).Where("share_token_id = ? AND status != ?", share.ID, models.FileStatusDeleted)
	if share.Mode != models.ShareModeBoth {
		query = query.Where("uploader_ip = ?", c.ClientIP())
	}

	var files []models.File
	query.Order("created_at DESC").Limit(300).Find(&files)

	c.JSON(http.StatusOK, gin.H{"success": true, "data": files})
}

// shareFile 查询分享下的文件
func shareFile(c *gin.Context, share *models.ShareToken) (*models.File, bool) {
	var file models.File
	if err := database.DB.Where("id = ? AND share_token_id = ?", c.Param("id"), share.ID).First(&file).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"code": "NOT_FOUND", "message": "文件不存在"},
		})
		return nil, false
	}
	return &file, true
}

// ShareConfirm 确认上传（使用 PUT 链接上传后调用）
func ShareConfirm(c *gin.Context) {
	share, ok := findValidShareToken(c, c.Param("token"))
	if !ok {
		return
	}
	file, ok := shareFile(c, share)
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
	if status == http.StatusOK {
		// 次数在生成签名时已累计，这里只补计容量
		database.DB.Model(&models.ShareToken{}).Where("id = ?", share.ID).
			UpdateColumn("used_size", gorm.Expr("used_size + ?", file.Size))
	}
	c.JSON(status, body)
}

// ShareDownload 获取分享文件的下载链接
func ShareDownload(c *gin.Context) {
	share, ok := findValidShareToken(c, c.Param("token"))
	if !ok {
		return
	}
	file, ok := shareFile(c, share)
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

// ShareDeleteFile 分享内删除自己的文件
func ShareDeleteFile(c *gin.Context) {
	share, ok := findValidShareToken(c, c.Param("token"))
	if !ok {
		return
	}
	file, ok := shareFile(c, share)
	if !ok {
		return
	}
	// 只允许删除本设备上传的文件（按上传时记录的 IP 判定），
	// 避免拿到分享链接的人误删别人的文件。
	if file.UploaderIP != "" && file.UploaderIP != c.ClientIP() {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"error":   gin.H{"code": "FORBIDDEN", "message": "只能删除本设备上传的文件"},
		})
		return
	}

	if err := services.R2Service.DeleteObject(c.Request.Context(), file.Key); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "DELETE_FAILED", "message": "删除存储对象失败"},
		})
		return
	}
	database.DB.Delete(file)

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "文件已删除"})
}

// ShareURLDownload 分享内 URL 下载任务
func ShareURLDownload(c *gin.Context) {
	share, ok := findValidShareToken(c, c.Param("token"))
	if !ok {
		return
	}
	if !share.CanUpload() {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"error":   gin.H{"code": "QUOTA_EXCEEDED", "message": "上传次数或容量已达上限"},
		})
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

	if req.Remark == "" {
		req.Remark = share.Description
	}

	task, err := createDownloadTask(share.UserID, req, nil, &share.ID)
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

// ShareListTasks 分享内任务列表
func ShareListTasks(c *gin.Context) {
	share, ok := findValidShareToken(c, c.Param("token"))
	if !ok {
		return
	}

	var tasks []models.DownloadTask
	database.DB.Where("share_token_id = ?", share.ID).Order("created_at DESC").Limit(50).Find(&tasks)

	c.JSON(http.StatusOK, gin.H{"success": true, "data": toTaskViews(tasks)})
}

// ShareDeleteTask 分享内删除任务
func ShareDeleteTask(c *gin.Context) {
	share, ok := findValidShareToken(c, c.Param("token"))
	if !ok {
		return
	}

	var task models.DownloadTask
	if err := database.DB.Where("id = ? AND share_token_id = ?", c.Param("id"), share.ID).First(&task).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"code": "NOT_FOUND", "message": "任务不存在"},
		})
		return
	}

	if services.IsRunning(task.ID) {
		services.CancelTask(task.ID)
	}
	database.DB.Delete(&task)

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "任务已删除"})
}

// ===== 辅助函数 =====

func ensureDeviceCookie(c *gin.Context) string {
	if existing, err := c.Cookie(deviceCookieName); err == nil && len(existing) >= 16 {
		return existing
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(deviceCookieName, id, 86400*365, "/", "", c.Request.TLS != nil, true)
	return id
}

func deviceID(c *gin.Context) string {
	if v, err := c.Cookie(deviceCookieName); err == nil {
		return v
	}
	return ""
}

func shouldCompressUpload(contentType, filename string) bool {
	switch strings.ToLower(pathExt(filename)) {
	case ".gz", ".tgz", ".zip", ".7z", ".rar", ".bz2", ".xz", ".zst",
		".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".heic",
		".mp3", ".mp4", ".m4a", ".mkv", ".mov", ".avi", ".flv", ".webm",
		".woff", ".woff2", ".ttf", ".otf", ".eot",
		".pdf", ".docx", ".xlsx", ".pptx", ".apk", ".ipa", ".exe", ".dmg", ".iso":
		return false
	}

	ct := strings.ToLower(contentType)
	for _, prefix := range []string{"text/", "application/json", "application/javascript",
		"application/xml", "application/sql", "image/svg", "application/x-sh", "application/x-httpd-php"} {
		if strings.Contains(ct, prefix) {
			return true
		}
	}
	return false
}

func pathExt(filename string) string {
	idx := strings.LastIndex(filename, ".")
	if idx <= 0 {
		return ""
	}
	return filename[idx:]
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func maxInt(v, min int) int {
	if v < min {
		return min
	}
	return v
}

func maxInt64(v, min int64) int64 {
	if v < min {
		return min
	}
	return v
}

func uploadErrorMessage(err error, maxSize int64) string {
	msg := err.Error()
	if strings.Contains(msg, "超过大小限制") && maxSize > 0 {
		return fmt.Sprintf("文件超过大小限制（最大 %s）", models.FormatBytes(maxSize))
	}
	return msg
}
