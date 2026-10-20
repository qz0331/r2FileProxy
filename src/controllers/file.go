package controllers

import (
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"r2proxy/src/database"
	"r2proxy/src/middleware"
	"r2proxy/src/models"
	"r2proxy/src/services"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	// 列表分页上限
	maxPageSize = 100
	// 单文件下载次数上限
	maxDownloadCount = 10
	// 下载链接有效期
	downloadURLTTL = 5 * time.Minute
	// 分享页下载链接有效期
	shareDownloadTTL = 30 * time.Minute
)

// generateR2Key 生成R2存储key，格式: {prefix}/{userId}/{baseName}_{yyMMddHHmmss}{ext}
func generateR2Key(prefix string, userID uint, filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	// 扩展名只保留安全字符
	if len(ext) > 16 || !regexp.MustCompile(`^\.[A-Za-z0-9]+$`).MatchString(ext) {
		ext = ""
	}
	baseName := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))

	// 清理文件名，只保留字母、数字、中文、下划线、连字符、点
	re := regexp.MustCompile(`[^\p{Han}\p{L}\p{N}_.\-]`)
	baseName = re.ReplaceAllString(baseName, "")
	baseName = strings.Trim(baseName, "._-")
	if baseName == "" {
		baseName = "file"
	}
	if runes := []rune(baseName); len(runes) > 80 {
		baseName = string(runes[:80])
	}

	// 时间戳 + 4位随机串，避免同名文件互相覆盖
	timestamp := time.Now().Format("060102150405")
	suffix := randomSuffix(4)

	if prefix == "" {
		prefix = "uploads"
	}
	return fmt.Sprintf("%s/%d/%s_%s%s%s", strings.Trim(prefix, "/"), userID, baseName, timestamp, suffix, ext)
}

func randomSuffix(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "0000"
	}
	for i := range b {
		b[i] = chars[int(buf[i])%len(chars)]
	}
	return string(b)
}

// streamCopy 将 R2 响应流式写入客户端
func streamCopy(c *gin.Context, src io.Reader) error {
	_, err := io.Copy(c.Writer, src)
	return err
}

// allLiveFileKeys 所有账号中未删除的文件 key（用于与桶内对象做差集，
// 因为 R2 桶是全局的，不只属于当前登录用户）
func allLiveFileKeys() []string {
	var keys []string
	database.DB.Model(&models.File{}).
		Where("deleted_at IS NULL").
		Pluck("key", &keys)
	return keys
}

// Index 首页（概览 + 上传 + 最近文件）
func Index(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.Redirect(http.StatusFound, middleware.BasePathOf(c)+"/login")
		return
	}

	// 兼容旧链接：/?filename=x&share_id=1&token_id=2 跳转到新的文件页
	if c.Query("filename") != "" || c.Query("share_id") != "" || c.Query("token_id") != "" {
		query := c.Request.URL.Query()
		c.Redirect(http.StatusFound, middleware.BasePathOf(c)+"/files?"+query.Encode())
		return
	}

	// 最近 8 个文件，完整列表在 /files 页面
	var recent []models.File
	database.DB.Where("user_id = ? AND deleted_at IS NULL AND status <> ?", user.ID, models.FileStatusDeleted).
		Order("created_at DESC").Limit(8).Find(&recent)

	stats := collectFileStats(user.ID)

	var shareCount int64
	database.DB.Model(&models.ShareToken{}).
		Where("user_id = ? AND revoked = ? AND expires_at > ?", user.ID, false, time.Now()).
		Count(&shareCount)

	// R2 官方用量：ListObjectsV2 遍历桶内对象累加真实大小（缓存 2 分钟）
	bu := services.R2Service.BucketUsage(c.Request.Context())
	usage := bu.DiffWithKeys(allLiveFileKeys(), 5)

	c.HTML(http.StatusOK, "index.html", gin.H{
		"title":      "概览",
		"user":       webUser(user),
		"active":     "home",
		"recent":     recent,
		"stats":      stats,
		"shareCount": shareCount,
		"usage":      usage,
		"basePath":   middleware.BasePathOf(c),
	})
}

// ListFiles 获取文件列表（API）
func ListFiles(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	filter := parseFileFilter(c)
	var total int64
	filter.apply(database.DB.Model(&models.File{}), user.ID, false).Count(&total)

	var files []models.File
	filter.apply(database.DB.Model(&models.File{}), user.ID, false).
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Find(&files)

	totalPages := int(total) / filter.PageSize
	if int(total)%filter.PageSize > 0 {
		totalPages++
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"files":       files,
			"total":       total,
			"page":        filter.Page,
			"page_size":   filter.PageSize,
			"total_pages": totalPages,
			"query":       filter.Q,
			"status":      filter.Status,
			"sort":        filter.Sort,
		},
	})
}

// confirmFileRecord 通过 HeadObject 更新文件大小、ETag 与状态
func confirmFileRecord(c *gin.Context, file *models.File) (int, gin.H) {
	head, err := services.R2Service.HeadObject(c.Request.Context(), file.Key)
	if err != nil {
		file.Status = models.FileStatusFailed
		database.DB.Save(file)
		return http.StatusBadRequest, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "UPLOAD_FAILED",
				"message": "文件上传失败，未在存储中找到",
			},
		}
	}

	file.Status = models.FileStatusConfirmed
	if head.ContentLength != nil {
		file.StoredSize = *head.ContentLength
		if file.Size == 0 {
			file.Size = *head.ContentLength
		}
	}
	if head.ETag != nil {
		file.ETag = *head.ETag
	}
	if head.ContentType != nil {
		file.ContentType = *head.ContentType
	}
	if head.ContentEncoding != nil {
		file.ContentEncoding = *head.ContentEncoding
	}

	if err := database.DB.Save(file).Error; err != nil {
		return http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "更新文件状态失败"},
		}
	}

	return http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"file": gin.H{
				"id":               file.ID,
				"key":              file.Key,
				"status":           file.Status,
				"size":             file.Size,
				"stored_size":      file.StoredSize,
				"etag":             file.ETag,
				"content_type":     file.ContentType,
				"content_encoding": file.ContentEncoding,
			},
		},
	}
}

// ConfirmUpload 确认上传完成
func ConfirmUpload(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var file models.File
	if err := database.DB.Where("id = ? AND user_id = ?", c.Param("id"), user.ID).First(&file).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"code": "NOT_FOUND", "message": "文件记录不存在"},
		})
		return
	}

	if file.Status != models.FileStatusPending && file.Status != models.FileStatusFailed {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_REQUEST", "message": "文件状态不正确"},
		})
		return
	}

	status, body := confirmFileRecord(c, &file)
	c.JSON(status, body)
}

// DeleteFile 删除文件（同时删除 R2 对象）
func DeleteFile(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var file models.File
	if err := database.DB.Where("id = ? AND user_id = ?", c.Param("id"), user.ID).First(&file).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"code": "NOT_FOUND", "message": "文件不存在"},
		})
		return
	}

	// 先删 R2，失败则保留记录以便重试
	if err := services.R2Service.DeleteObject(c.Request.Context(), file.Key); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "DELETE_FAILED", "message": "删除存储对象失败，请稍后重试"},
		})
		return
	}
	database.DB.Delete(&file)

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "文件已删除"})
}

// BatchDeleteFile 批量删除文件
func BatchDeleteFile(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var req struct {
		IDs []uint `json:"ids" binding:"required,min=1,max=200"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_REQUEST", "message": "请求参数错误"},
		})
		return
	}

	var files []models.File
	database.DB.Where("id IN ? AND user_id = ?", req.IDs, user.ID).Find(&files)

	success, failed := 0, 0
	var failedIDs []uint
	for _, f := range files {
		if err := services.R2Service.DeleteObject(c.Request.Context(), f.Key); err != nil {
			failed++
			failedIDs = append(failedIDs, f.ID)
			continue
		}
		database.DB.Delete(&f)
		success++
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"deleted":    success,
			"failed":     failed,
			"failed_ids": failedIDs,
		},
		"message": fmt.Sprintf("删除完成：成功 %d，失败 %d", success, failed),
	})
}

// UpdateRemarkRequest 更新备注请求
type UpdateRemarkRequest struct {
	Remark string `json:"remark" binding:"max=256"`
}

// validateFilename 校验文件名
func validateFilename(filename string) error {
	if len(filename) == 0 {
		return fmt.Errorf("文件名不能为空")
	}
	if len([]rune(filename)) > 200 {
		return fmt.Errorf("文件名过长")
	}
	for _, d := range []string{"..", "/", "\\", "\x00", "\r", "\n"} {
		if strings.Contains(filename, d) {
			return fmt.Errorf("文件名包含非法字符")
		}
	}
	if strings.TrimSpace(filename) == "" {
		return fmt.Errorf("文件名不能为空")
	}
	return nil
}

// UpdateRemark 更新文件备注
func UpdateRemark(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var req UpdateRemarkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_REQUEST", "message": "请求参数错误"},
		})
		return
	}

	var file models.File
	if err := database.DB.Where("id = ? AND user_id = ?", c.Param("id"), user.ID).First(&file).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"code": "NOT_FOUND", "message": "文件不存在"},
		})
		return
	}

	file.Remark = strings.TrimSpace(req.Remark)
	if err := database.DB.Save(&file).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "更新备注失败"},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"remark": file.Remark}})
}

// contentDisposition 生成兼容中文文件名的下载头
func contentDisposition(filename string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, filename)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, url.PathEscape(filename))
}

// downloadFileRecord 校验所有权并生成下载链接
func downloadFileRecord(c *gin.Context, file *models.File, ttl time.Duration) (string, int, gin.H) {
	url, err := services.R2Service.GenerateDownloadURLWithDisposition(
		c.Request.Context(),
		file.Key,
		ttl,
		contentDisposition(file.Filename),
	)
	if err != nil {
		return "", http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "生成下载链接失败"},
		}
	}
	return url, http.StatusOK, nil
}

// ProxyDownload 代理下载（支持次数限制）
func ProxyDownload(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var file models.File
	if err := database.DB.Where("id = ? AND user_id = ? AND status = ?", c.Param("id"), user.ID, models.FileStatusConfirmed).First(&file).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"code": "NOT_FOUND", "message": "文件不存在"},
		})
		return
	}

	if file.DownloadCount >= maxDownloadCount {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"error":   gin.H{"code": "DOWNLOAD_LIMIT_EXCEEDED", "message": fmt.Sprintf("下载次数已达上限（%d次）", maxDownloadCount)},
		})
		return
	}

	downloadURL, status, body := downloadFileRecord(c, &file, downloadURLTTL)
	if status != http.StatusOK {
		c.JSON(status, body)
		return
	}

	resp, err := http.Get(downloadURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "下载失败"},
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "文件不存在或已删除"},
		})
		return
	}

	file.DownloadCount++
	database.DB.Save(&file)

	c.Header("Content-Disposition", contentDisposition(file.Filename))
	if file.ContentType != "" {
		c.Header("Content-Type", file.ContentType)
	}
	if resp.ContentLength > 0 {
		c.Header("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	_ = streamCopy(c, resp.Body)
}

// GetDownloadURL 获取文件的预签名下载 URL
func GetDownloadURL(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var file models.File
	if err := database.DB.Where("id = ? AND user_id = ? AND status = ?", c.Param("id"), user.ID, models.FileStatusConfirmed).First(&file).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"code": "NOT_FOUND", "message": "文件不存在"},
		})
		return
	}

	downloadURL, status, body := downloadFileRecord(c, &file, downloadURLTTL)
	if status != http.StatusOK {
		c.JSON(status, body)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"download_url":     downloadURL,
			"filename":         file.Filename,
			"size":             file.Size,
			"public_url":       services.R2Service.PublicURL(file.Key),
			"expires_in":       int(downloadURLTTL.Seconds()),
			"download_count":   file.DownloadCount,
			"download_limit":   maxDownloadCount,
			"content_type":     file.ContentType,
			"content_encoding": file.ContentEncoding,
		},
	})
}
