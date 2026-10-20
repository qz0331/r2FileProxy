package controllers

import (
	"net/http"
	"r2proxy/src/database"
	"r2proxy/src/middleware"
	"r2proxy/src/models"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// UserPage 用户管理页面
func UserPage(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var users []models.User
	database.DB.Order("created_at DESC").Find(&users)

	type userRow struct {
		ID        uint
		Username  string
		APIKey    string
		IsAdmin   bool
		UserType  string
		CreatedAt string
	}
	rows := make([]userRow, 0, len(users))
	for _, u := range users {
		rows = append(rows, userRow{
			ID:        u.ID,
			Username:  u.Username,
			APIKey:    u.APIKey,
			IsAdmin:   u.IsAdministrator(),
			UserType:  u.UserType,
			CreatedAt: u.CreatedAt.Format("2006-01-02"),
		})
	}

	c.HTML(http.StatusOK, "users.html", gin.H{
		"title":    "用户管理",
		"user":     webUser(user),
		"active":   "users",
		"users":    rows,
		"basePath": middleware.BasePathOf(c),
	})
}

// CreateUserRequest 创建用户请求
type CreateUserRequest struct {
	Username string `json:"username" binding:"required,min=3,max=64"`
	Password string `json:"password" binding:"required,min=15"`
	UserType string `json:"user_type"` // web / api / both，默认 both
}

// CreateUser 创建用户（仅管理员）
func CreateUser(c *gin.Context) {
	var req CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_REQUEST", "message": "请求参数错误"},
		})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_REQUEST", "message": "用户名不能为空"},
		})
		return
	}

	if err := validatePasswordComplexity(req.Password); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "WEAK_PASSWORD", "message": err.Error()},
		})
		return
	}

	userType := req.UserType
	switch userType {
	case models.UserTypeWeb, models.UserTypeAPI, models.UserTypeBoth:
	default:
		userType = models.UserTypeBoth
	}

	var existing models.User
	if err := database.DB.Where("username = ?", req.Username).First(&existing).Error; err == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "USERNAME_EXISTS", "message": "用户名已存在"},
		})
		return
	}

	var userCount int64
	database.DB.Model(&models.User{}).Count(&userCount)
	isFirstUser := userCount == 0

	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "密码加密失败"},
		})
		return
	}

	role := models.RoleUser
	if isFirstUser {
		role = models.RoleAdmin
	}

	user := models.User{
		Username:     req.Username,
		PasswordHash: string(hashed),
		APIKey:       generateAPIKey(),
		UserType:     userType,
		Role:         role,
		// is_admin 为历史字段，保持与 role 一致
		IsAdmin: isFirstUser,
	}

	if err := database.DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "INTERNAL_ERROR", "message": "创建用户失败"},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"id":       user.ID,
			"username": user.Username,
			"is_admin": isFirstUser,
			"api_key":  user.APIKey,
		},
	})
}

// ListUsers 列出用户（仅管理员）
func ListUsers(c *gin.Context) {
	var users []models.User
	database.DB.Order("created_at DESC").Find(&users)

	type userDTO struct {
		ID        uint   `json:"id"`
		Username  string `json:"username"`
		APIKey    string `json:"api_key"`
		UserType  string `json:"user_type"`
		IsAdmin   bool   `json:"is_admin"`
		CreatedAt string `json:"created_at"`
	}
	out := make([]userDTO, 0, len(users))
	for _, u := range users {
		out = append(out, userDTO{
			ID:        u.ID,
			Username:  u.Username,
			APIKey:    u.APIKey,
			UserType:  u.UserType,
			IsAdmin:   u.IsAdministrator(),
			CreatedAt: u.CreatedAt.Format("2006-01-02 15:04"),
		})
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}

// DeleteUser 删除用户（仅管理员）
func DeleteUser(c *gin.Context) {
	currentUser := middleware.GetCurrentUser(c)
	targetID := parseID(c.Param("id"))
	if targetID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_REQUEST", "message": "用户 ID 无效"},
		})
		return
	}
	if currentUser.ID == targetID {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "CANNOT_DELETE_SELF", "message": "不能删除自己的账号"},
		})
		return
	}

	var user models.User
	if err := database.DB.First(&user, targetID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"code": "NOT_FOUND", "message": "用户不存在"},
		})
		return
	}

	// 管理员数量保护：至少保留一个管理员
	if user.IsAdministrator() {
		var adminCount int64
		database.DB.Model(&models.User{}).
			Where("is_admin = ? OR role = ?", true, models.RoleAdmin).
			Count(&adminCount)
		if adminCount <= 1 {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   gin.H{"code": "LAST_ADMIN", "message": "至少需要保留一个管理员账号"},
			})
			return
		}
	}

	// 用户文件记录随用户删除（R2 对象保留，避免误删）
	database.DB.Delete(&user)

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "用户已删除"})
}

// parseUint 解析 uint（兼容旧调用）
func parseUint(s string) uint {
	return parseID(s)
}
