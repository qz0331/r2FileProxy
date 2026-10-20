package controllers

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"r2proxy/src/middleware"
	"r2proxy/src/models"
	"strings"

	"github.com/gin-gonic/gin"
)

// PutPage 通用 PUT 上传页：让手机/平板也能使用 Sign PUT 链接（无需登录）
func PutPage(c *gin.Context) {
	c.HTML(http.StatusOK, "put.html", gin.H{
		"title":    "PUT 上传",
		"barTitle": "PUT 上传",
		"basePath": middleware.BasePathOf(c),
		"putURL":   c.Query("url"),
	})
}

// PutProxy 将文件转发到指定的 R2 预签名 PUT 地址
//
// 安全约束：仅允许 https + *.r2.cloudflarestorage.com，避免被当作任意请求代理。
func PutProxy(c *gin.Context) {
	rawURL := strings.TrimSpace(c.Query("url"))
	if rawURL == "" {
		rawURL = strings.TrimSpace(c.GetHeader("X-Put-Url"))
	}
	if rawURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "MISSING_URL", "message": "缺少 PUT 地址"},
		})
		return
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_URL", "message": "只支持 https 的 R2 预签名地址"},
		})
		return
	}
	host := strings.ToLower(parsed.Hostname())
	if !strings.HasSuffix(host, ".r2.cloudflarestorage.com") && !strings.HasSuffix(host, ".amazonaws.com") {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "URL_NOT_ALLOWED", "message": "只允许 R2/S3 预签名地址"},
		})
		return
	}

	filename := c.Query("filename")
	if filename != "" {
		if err := validateFilename(filename); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   gin.H{"code": "INVALID_FILENAME", "message": err.Error()},
			})
			return
		}
	}

	contentType := c.GetHeader("X-Content-Type")
	if contentType == "" {
		contentType = c.ContentType()
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPut, rawURL, c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_REQUEST", "message": "创建上传请求失败"},
		})
		return
	}
	req.ContentLength = c.Request.ContentLength
	if c.Request.ContentLength > 0 {
		req.ContentLength = c.Request.ContentLength
	}
	// 预签名 URL 只签了 host，额外头不会影响签名
	req.Header.Set("Content-Type", contentType)

	resp, err := httpClientForProxy().Do(req)
	if err != nil {
		log.Printf("PUT 转发失败: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"error":   gin.H{"code": "UPSTREAM_ERROR", "message": "上传到 R2 失败，请检查链接是否过期"},
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		log.Printf("PUT 转发返回 %d: %s", resp.StatusCode, string(body))
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "UPSTREAM_REJECTED",
				"message": fmt.Sprintf("R2 拒绝了上传（HTTP %d），链接可能已过期", resp.StatusCode),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"filename":     filename,
			"size":         c.Request.ContentLength,
			"content_type": contentType,
			"etag":         resp.Header.Get("ETag"),
		},
		"message": "上传成功",
	})
}

// UploadPageHandler 供上传页查询可选信息
func PutInfo(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"max_upload_size": func() int64 {
				if max := maxUploadSize(); max > 0 {
					return max
				}
				return 0
			}(),
			"human_max_size": models.FormatBytes(maxUploadSize()),
		},
	})
}

// 复用 services 中的客户端，但避免跨包循环依赖，这里单独保持一个连接池
var proxyHTTPClient = &http.Client{}

func httpClientForProxy() *http.Client {
	return proxyHTTPClient
}
