package controllers

import (
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

// AuthUpload 代理上传（登录用户）：
//   - 浏览器只与本服务通信，不依赖 R2 的 CORS 配置；
//   - 大文件自动分片上传，文本类文件自动 gzip 压缩以节省存储；
//   - 上传完成后直接入库（confirmed）。
func AuthUpload(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   gin.H{"code": "UNAUTHORIZED", "message": "用户未认证"},
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

	maxSize := config.AppConfig.MaxUploadSize
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

	contentType := c.GetHeader("X-Content-Type")
	if contentType == "" {
		contentType = c.ContentType()
	}
	if contentType == "" || len(contentType) > 128 {
		contentType = "application/octet-stream"
	}

	// 默认自动判断是否压缩，可用 ?compress=0 关闭
	compress := true
	if v := c.Query("compress"); v == "0" || v == "false" {
		compress = false
	}
	compress = compress && shouldCompressUpload(contentType, filename)

	prefix := sanitizePrefix(c.Query("prefix"))
	if prefix == "" {
		prefix = "uploads"
	}

	remark := strings.TrimSpace(c.Query("remark"))
	if remark == "" {
		remark = filename
	}

	key := generateR2Key(prefix, user.ID, filename)

	streamResult, err := services.R2Service.PutObjectStream(
		c.Request.Context(),
		key,
		c.Request.Body,
		contentType,
		compress,
		maxSize,
		nil,
	)
	if err != nil {
		log.Printf("代理上传失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "UPLOAD_FAILED", "message": uploadErrorMessage(err, maxSize)},
		})
		return
	}

	file := models.File{
		UserID:          user.ID,
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
			},
			"compressed": streamResult.Compressed,
		},
	})
}

// TempUploadProxy 临时授权代理上传
func TempUploadProxy(c *gin.Context) {
	token, ok := findValidTempToken(c, c.Param("token"))
	if !ok {
		return
	}

	var owner models.User
	if err := database.DB.First(&owner, token.UserID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "授权用户不存在"},
		})
		return
	}

	proxyUploadFor(c, &owner, nil, &token.ID, "temp", token.Description)
}

// proxyUploadFor 通用代理上传实现
func proxyUploadFor(c *gin.Context, owner *models.User, shareID, tokenID *uint, prefix, defaultRemark string) {
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

	maxSize := config.AppConfig.MaxUploadSize
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

	contentType := c.GetHeader("X-Content-Type")
	if contentType == "" {
		contentType = c.ContentType()
	}
	if contentType == "" || len(contentType) > 128 {
		contentType = "application/octet-stream"
	}

	compress := shouldCompressUpload(contentType, filename)
	key := generateR2Key(prefix, owner.ID, filename)

	streamResult, err := services.R2Service.PutObjectStream(
		c.Request.Context(), key, c.Request.Body, contentType, compress, maxSize, nil,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "UPLOAD_FAILED", "message": uploadErrorMessage(err, maxSize)},
		})
		return
	}

	remark := defaultRemark
	if remark == "" {
		remark = filename
	}

	file := models.File{
		UserID:           owner.ID,
		TemporaryTokenID: tokenID,
		ShareTokenID:     shareID,
		Key:              key,
		Filename:         filename,
		Remark:           truncateRunes(remark, 256),
		Size:             streamResult.RawSize,
		StoredSize:       streamResult.StoredSize,
		ContentType:      contentType,
		ContentEncoding:  streamResult.ContentEncoding,
		ETag:             streamResult.ETag,
		Status:           models.FileStatusConfirmed,
		UploaderIP:       c.ClientIP(),
	}
	if err := database.DB.Create(&file).Error; err != nil {
		_ = services.R2Service.DeleteObject(c.Request.Context(), key)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "保存文件记录失败"},
		})
		return
	}

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
			},
			"compressed": streamResult.Compressed,
		},
	})
}
