package controllers

import (
	"net/http"
	"r2proxy/src/database"
	"r2proxy/src/middleware"
	"r2proxy/src/models"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 文件列表排序方式
const (
	sortNewest   = "newest"
	sortOldest   = "oldest"
	sortName     = "name"
	sortSizeDesc = "size_desc"
	sortSizeAsc  = "size_asc"
)

// fileFilter 文件列表筛选条件
type fileFilter struct {
	Q        string
	Status   string
	From     string
	To       string
	Sort     string
	Page     int
	PageSize int
	TokenID  string
	ShareID  string
}

// parseFileFilter 解析查询参数（兼容 q / filename 两种搜索参数）
func parseFileFilter(c *gin.Context) fileFilter {
	f := fileFilter{
		Q:       strings.TrimSpace(c.Query("q")),
		Status:  c.DefaultQuery("status", "all"),
		From:    strings.TrimSpace(c.Query("from")),
		To:      strings.TrimSpace(c.Query("to")),
		Sort:    c.DefaultQuery("sort", sortNewest),
		TokenID: strings.TrimSpace(c.Query("token_id")),
		ShareID: strings.TrimSpace(c.Query("share_id")),
	}
	if f.Q == "" {
		f.Q = strings.TrimSpace(c.Query("filename"))
	}

	f.Page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	if f.Page < 1 {
		f.Page = 1
	}
	f.PageSize, _ = strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if f.PageSize < 1 || f.PageSize > maxPageSize {
		f.PageSize = 20
	}

	switch f.Status {
	case models.FileStatusConfirmed, models.FileStatusPending, models.FileStatusFailed:
	default:
		f.Status = "all"
	}
	switch f.Sort {
	case sortNewest, sortOldest, sortName, sortSizeDesc, sortSizeAsc:
	default:
		f.Sort = sortNewest
	}
	return f
}

// escapeLike 转义 LIKE 中的通配符，避免用户输入 % 或 _ 被当作模式
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

// parseDate 解析 YYYY-MM-DD（本地时区）
func parseDate(value string, endOfDay bool) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	if endOfDay {
		t = t.Add(24*time.Hour - time.Nanosecond)
	}
	return t, true
}

// apply 把筛选条件应用到查询
func (f fileFilter) apply(query *gorm.DB, userID uint, includeDeleted bool) *gorm.DB {
	query = query.Where("user_id = ?", userID)
	if !includeDeleted {
		query = query.Where("deleted_at IS NULL AND status <> ?", models.FileStatusDeleted)
	}

	if f.Status != "all" {
		query = query.Where("status = ?", f.Status)
	}
	if f.Q != "" {
		like := escapeLike(f.Q)
		query = query.Where(`(filename LIKE ? ESCAPE '\' OR remark LIKE ? ESCAPE '\')`, like, like)
	}
	if from, ok := parseDate(f.From, false); ok {
		query = query.Where("created_at >= ?", from)
	}
	if to, ok := parseDate(f.To, true); ok {
		query = query.Where("created_at <= ?", to)
	}
	if f.TokenID != "" {
		query = query.Where("temporary_token_id = ?", f.TokenID)
	}
	if f.ShareID != "" {
		query = query.Where("share_token_id = ?", f.ShareID)
	}

	switch f.Sort {
	case sortOldest:
		query = query.Order("created_at ASC")
	case sortName:
		query = query.Order("filename ASC")
	case sortSizeDesc:
		query = query.Order("size DESC")
	case sortSizeAsc:
		query = query.Order("size ASC")
	default:
		query = query.Order("created_at DESC")
	}
	return query
}

// fileListStats 列表页需要的统计
type fileListStats struct {
	Total     int64
	Size      int64
	Stored    int64
	Confirmed int64
	Pending   int64
	Failed    int64
}

// collectFileStats 汇总当前用户的文件统计
func collectFileStats(userID uint) fileListStats {
	var s fileListStats
	database.DB.Model(&models.File{}).
		Select("COUNT(*) as total, COALESCE(SUM(size),0) as size, COALESCE(SUM(stored_size),0) as stored, "+
			"SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) as confirmed, "+
			"SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) as pending, "+
			"SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) as failed",
			models.FileStatusConfirmed, models.FileStatusPending, models.FileStatusFailed).
		Where("user_id = ? AND deleted_at IS NULL AND status <> ?", userID, models.FileStatusDeleted).
		Scan(&s)
	return s
}

// FilesPage 文件管理页（独立页面，支持模糊搜索与筛选）
func FilesPage(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.Redirect(http.StatusFound, middleware.BasePathOf(c)+"/login")
		return
	}

	filter := parseFileFilter(c)

	var total int64
	filter.apply(database.DB.Model(&models.File{}), user.ID, false).Count(&total)

	totalPages := int(total) / filter.PageSize
	if int(total)%filter.PageSize > 0 {
		totalPages++
	}
	if totalPages == 0 {
		totalPages = 1
	}
	if filter.Page > totalPages {
		filter.Page = totalPages
	}

	var files []models.File
	filter.apply(database.DB.Model(&models.File{}), user.ID, false).
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Find(&files)

	// 筛选来源名称
	var tokenDesc, shareDesc string
	if filter.TokenID != "" {
		var token models.TemporaryToken
		if err := database.DB.Where("id = ? AND user_id = ?", filter.TokenID, user.ID).First(&token).Error; err == nil {
			tokenDesc = token.Description
		}
	}
	if filter.ShareID != "" {
		var share models.ShareToken
		if err := database.DB.Where("id = ? AND user_id = ?", filter.ShareID, user.ID).First(&share).Error; err == nil {
			shareDesc = share.Name
		}
	}

	c.HTML(http.StatusOK, "files.html", gin.H{
		"title":      "全部文件",
		"user":       webUser(user),
		"active":     "files",
		"files":      files,
		"total":      total,
		"page":       filter.Page,
		"pageSize":   filter.PageSize,
		"totalPages": totalPages,
		"filter":     filter,
		"tokenDesc":  tokenDesc,
		"shareDesc":  shareDesc,
		"stats":      collectFileStats(user.ID),
		"basePath":   middleware.BasePathOf(c),
	})
}
