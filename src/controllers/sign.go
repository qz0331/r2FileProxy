package controllers

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"r2proxy/src/config"
	"r2proxy/src/database"
	"r2proxy/src/middleware"
	"r2proxy/src/models"
	"r2proxy/src/services"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// 签名默认与最大有效期
const (
	defaultSignTTL = 24 * time.Hour
	maxSignTTL     = 7 * 24 * time.Hour
)

// SignUploadRequest 上传签名请求
type SignUploadRequest struct {
	Filename    string `json:"filename" binding:"required,min=1,max=256"`
	ContentType string `json:"content_type"`
	ExpiresIn   int    `json:"expires_in"` // 秒
	Remark      string `json:"remark" binding:"max=256"`
	Prefix      string `json:"prefix" binding:"max=128"`
}

// normalizeTTL 解析并限制签名有效期
func normalizeTTL(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultSignTTL
	}
	ttl := time.Duration(seconds) * time.Second
	if ttl > maxSignTTL {
		return maxSignTTL
	}
	return ttl
}

// pendingUploadResult 创建待确认上传后的结果
type pendingUploadResult struct {
	File      *models.File
	UploadURL string
	TTL       time.Duration
}

// newPendingUpload 创建待确认的文件记录并生成上传签名
func newPendingUpload(
	c *gin.Context,
	user *models.User,
	prefix string,
	filename string,
	contentType string,
	ttl time.Duration,
	tokenID *uint,
	shareID *uint,
	remark string,
) (*pendingUploadResult, int, gin.H) {
	if err := validateFilename(filename); err != nil {
		return nil, http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_FILENAME", "message": err.Error()},
		}
	}

	if contentType == "" || len(contentType) > 128 {
		contentType = "application/octet-stream"
	}
	if remark == "" {
		remark = filename
	}
	if len([]rune(remark)) > 256 {
		remark = string([]rune(remark)[:256])
	}

	key := generateR2Key(prefix, user.ID, filename)
	expiresAt := time.Now().Add(ttl)

	file := models.File{
		UserID:           user.ID,
		TemporaryTokenID: tokenID,
		ShareTokenID:     shareID,
		Key:              key,
		Filename:         filename,
		Remark:           remark,
		ContentType:      contentType,
		Status:           models.FileStatusPending,
		UploaderIP:       c.ClientIP(),
		UploadExpiresAt:  &expiresAt,
	}
	if err := database.DB.Create(&file).Error; err != nil {
		return nil, http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "创建文件记录失败"},
		}
	}

	// 签名不绑定 Content-Type，避免浏览器实际请求头不一致导致 SignatureDoesNotMatch
	uploadURL, err := services.R2Service.GenerateUploadURL(c.Request.Context(), key, ttl)
	if err != nil {
		log.Printf("生成上传签名失败: %v", err)
		database.DB.Delete(&file)
		return nil, http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "生成签名 URL 失败"},
		}
	}

	return &pendingUploadResult{File: &file, UploadURL: uploadURL, TTL: ttl}, http.StatusOK, nil
}

// maxUploadSize 返回代理上传大小上限（0 表示不限制）
func maxUploadSize() int64 {
	if config.AppConfig != nil {
		return config.AppConfig.MaxUploadSize
	}
	return 0
}

// SignUpload 获取上传签名 URL
func SignUpload(c *gin.Context) {
	var req SignUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_REQUEST", "message": "请求参数错误: " + err.Error()},
		})
		return
	}

	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   gin.H{"code": "UNAUTHORIZED", "message": "用户未认证"},
		})
		return
	}

	prefix := sanitizePrefix(req.Prefix)
	if prefix == "" {
		prefix = "uploads"
	}

	result, status, errResp := newPendingUpload(c, user, prefix, req.Filename, req.ContentType, normalizeTTL(req.ExpiresIn), nil, nil, req.Remark)
	if status != http.StatusOK {
		c.JSON(status, errResp)
		return
	}

	c.JSON(http.StatusOK, uploadPayload(c, result, nil))
}

// SignPutLinkRequest Sign PUT 链接请求
type SignPutLinkRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	ExpiresIn   int    `json:"expires_in"` // 秒
	Remark      string `json:"remark"`
	Prefix      string `json:"prefix"`
}

// SignPutLink 生成可直接 PUT 上传的签名链接（供其他设备/脚本使用）
//
// 与 /api/sign/upload 的差异：
//   - 默认有效期更长（24 小时，最长 7 天）；
//   - 返回可直接复制的 curl 命令与二维码内容，方便在手机/平板等设备使用；
//   - 上传完成后可在文件列表中点击“确认”入库（或调用 confirm 接口）。
func SignPutLink(c *gin.Context) {
	var req SignPutLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_REQUEST", "message": "请求参数错误"},
		})
		return
	}

	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   gin.H{"code": "UNAUTHORIZED", "message": "用户未认证"},
		})
		return
	}

	filename := strings.TrimSpace(req.Filename)
	if filename == "" {
		filename = fmt.Sprintf("upload-%s.bin", time.Now().Format("20060102-150405"))
	}

	prefix := sanitizePrefix(req.Prefix)
	if prefix == "" {
		prefix = "putlink"
	}

	result, status, errResp := newPendingUpload(c, user, prefix, filename, req.ContentType, normalizeTTL(req.ExpiresIn), nil, nil, req.Remark)
	if status != http.StatusOK {
		c.JSON(status, errResp)
		return
	}

	payload := uploadPayload(c, result, nil)
	payload["data"].(gin.H)["method"] = "PUT"
	payload["data"].(gin.H)["curl"] = fmt.Sprintf(
		"curl -X PUT -T \"<本地文件路径>\" --upload-file \"%s\" '%s'",
		filename, result.UploadURL,
	)
	payload["data"].(gin.H)["mobile_upload_url"] = middleware.BasePathOf(c) + "/put?url=" + url.QueryEscape(result.UploadURL)
	payload["data"].(gin.H)["instructions"] = []string{
		"1. 在任意设备执行 curl 命令（或使用支持 PUT 的工具）",
		"2. 上传成功后回到文件列表点击“确认”即可入库",
		"3. 链接过期后重新生成即可",
	}

	c.JSON(http.StatusOK, payload)
}

// uploadPayload 统一上传响应结构
func uploadPayload(c *gin.Context, result *pendingUploadResult, extra gin.H) gin.H {
	data := gin.H{
		"upload_url":      result.UploadURL,
		"put_url":         result.UploadURL,
		"key":             result.File.Key,
		"file_id":         result.File.ID,
		"filename":        result.File.Filename,
		"remark":          result.File.Remark,
		"expires_at":      result.File.UploadExpiresAt.Format(time.RFC3339),
		"expires_in":      int(result.TTL.Seconds()),
		"max_upload_size": maxUploadSize(),
		"confirm_url":     middleware.BasePathOf(c) + fmt.Sprintf("/api/files/%d/confirm", result.File.ID),
	}
	for k, v := range extra {
		data[k] = v
	}
	return gin.H{"success": true, "data": data}
}

// ListPutLinks 列出最近生成的 Sign PUT 链接（待确认文件）
func ListPutLinks(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var files []models.File
	database.DB.
		Where("user_id = ? AND status = ?", user.ID, models.FileStatusPending).
		Order("created_at DESC").
		Limit(50).
		Find(&files)

	type linkDTO struct {
		ID         uint   `json:"id"`
		Filename   string `json:"filename"`
		Key        string `json:"key"`
		ExpiresAt  string `json:"expires_at"`
		Expired    bool   `json:"expired"`
		CreatedAt  string `json:"created_at"`
		Size       int64  `json:"size"`
		UploaderIP string `json:"uploader_ip"`
	}
	out := make([]linkDTO, 0, len(files))
	for _, f := range files {
		expired := f.UploadExpiresAt != nil && time.Now().After(*f.UploadExpiresAt)
		expiresAt := ""
		if f.UploadExpiresAt != nil {
			expiresAt = f.UploadExpiresAt.Format(time.RFC3339)
		}
		out = append(out, linkDTO{
			ID:         f.ID,
			Filename:   f.Filename,
			Key:        f.Key,
			ExpiresAt:  expiresAt,
			Expired:    expired,
			CreatedAt:  f.CreatedAt.Format("2006-01-02 15:04"),
			Size:       f.Size,
			UploaderIP: f.UploaderIP,
		})
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}

// RegeneratePutLink 为待确认文件重新生成上传链接
func RegeneratePutLink(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var file models.File
	if err := database.DB.Where("id = ? AND user_id = ? AND status = ?", c.Param("id"), user.ID, models.FileStatusPending).First(&file).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"code": "NOT_FOUND", "message": "待确认文件不存在"},
		})
		return
	}

	ttl := defaultSignTTL
	expiresAt := time.Now().Add(ttl)
	file.UploadExpiresAt = &expiresAt
	database.DB.Model(&file).UpdateColumn("upload_expires_at", expiresAt)

	uploadURL, err := services.R2Service.GenerateUploadURL(c.Request.Context(), file.Key, ttl)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "生成签名 URL 失败"},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"upload_url": uploadURL,
			"put_url":    uploadURL,
			"key":        file.Key,
			"file_id":    file.ID,
			"filename":   file.Filename,
			"expires_at": expiresAt.Format(time.RFC3339),
			"expires_in": int(ttl.Seconds()),
			"curl":       fmt.Sprintf("curl -X PUT -T \"<本地文件路径>\" '%s'", uploadURL),
		},
	})
}

// SignDownloadRequest 下载签名请求
type SignDownloadRequest struct {
	Key       string `json:"key" binding:"required,min=1,max=512"`
	ExpiresIn int    `json:"expires_in"` // 秒
}

// SignDownload 获取下载签名 URL
func SignDownload(c *gin.Context) {
	var req SignDownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_REQUEST", "message": "请求参数错误"},
		})
		return
	}

	user := middleware.GetCurrentUser(c)

	var file models.File
	if err := database.DB.Where("key = ? AND user_id = ? AND status != ?", req.Key, user.ID, models.FileStatusDeleted).First(&file).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"code": "NOT_FOUND", "message": "文件不存在"},
		})
		return
	}

	head, err := services.R2Service.HeadObject(c.Request.Context(), req.Key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error": gin.H{
				"code":       "FILE_NOT_IN_R2",
				"message":    "文件在存储中不存在，可能已被删除",
				"file_id":    file.ID,
				"can_delete": true,
			},
		})
		return
	}

	ttl := normalizeTTL(req.ExpiresIn)
	downloadURL, err := services.R2Service.GenerateDownloadURLWithDisposition(
		c.Request.Context(),
		req.Key,
		ttl,
		contentDisposition(file.Filename),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "生成签名 URL 失败"},
		})
		return
	}

	size := file.Size
	if head.ContentLength != nil {
		size = *head.ContentLength
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"download_url": downloadURL,
			"filename":     file.Filename,
			"content_type": file.ContentType,
			"size":         size,
			"public_url":   services.R2Service.PublicURL(file.Key),
			"expires_at":   time.Now().Add(ttl).Format(time.RFC3339),
		},
	})
}

// sanitizePrefix 清理存储前缀
func sanitizePrefix(prefix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range prefix {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '/':
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "/")
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}
