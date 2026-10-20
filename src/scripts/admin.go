package scripts

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"strings"

	"r2proxy/src/database"
	"r2proxy/src/models"

	"golang.org/x/crypto/bcrypt"
)

// 默认值仅用于「未配置环境变量」时的本地首次启动，公开仓库不应依赖这些弱口令。
// 生产部署请通过 ADMIN_USERNAME / ADMIN_PASSWORD 指定，或首次登录后立即修改。
const (
	fallbackAdminUsername = "admin"
	// 随机生成的密码在启动日志中打印一次
	minPasswordLen = 15
)

// InitAdmin 初始化管理员账号
//
// 用户名/密码来源优先级：
//  1. 环境变量 ADMIN_USERNAME / ADMIN_PASSWORD
//  2. 未设置密码时自动生成 24 位随机密码，并打印到日志（只打印一次）
func InitAdmin() error {
	var count int64
	database.DB.Model(&models.User{}).Count(&count)
	if count > 0 {
		log.Println("用户已存在，跳过初始化")
		return nil
	}

	username := strings.TrimSpace(os.Getenv("ADMIN_USERNAME"))
	if username == "" {
		username = fallbackAdminUsername
	}

	password := os.Getenv("ADMIN_PASSWORD")
	generated := false
	if password == "" {
		password = randomPassword(24)
		generated = true
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	apiKey := generateAPIKey()

	admin := models.User{
		Username:     username,
		PasswordHash: string(hashedPassword),
		APIKey:       apiKey,
		UserType:     models.UserTypeBoth,
		Role:         models.RoleAdmin,
		IsAdmin:      true,
	}

	if err := database.DB.Create(&admin).Error; err != nil {
		return err
	}

	log.Println("==========================================")
	log.Println("管理员账号初始化成功")
	log.Printf("用户名: %s", username)
	if generated {
		log.Printf("密码: %s", password)
		log.Println("（以上密码为一次性随机生成，请立即保存并修改）")
	} else {
		log.Println("密码: 来自环境变量 ADMIN_PASSWORD")
	}
	log.Printf("API Key: %s", apiKey)
	log.Println("请妥善保管以上信息，并立即修改密码！")
	log.Println("==========================================")

	return nil
}

// randomPassword 生成满足复杂度要求的随机密码。
func randomPassword(length int) string {
	const (
		upper   = "ABCDEFGHJKLMNPQRSTUVWXYZ"
		lower   = "abcdefghijkmnopqrstuvwxyz"
		digits  = "23456789"
		special = "!@#$%^&*-_=+?"
	)
	all := upper + lower + digits + special

	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand 不可用: " + err.Error())
	}

	out := make([]byte, 0, length)
	// 先各取一个，确保四类字符都存在
	for _, set := range []string{upper, lower, digits, special} {
		out = append(out, set[int(buf[len(out)])%len(set)])
	}
	for len(out) < length {
		out = append(out, all[int(buf[len(out)])%len(all)])
	}
	// 打乱顺序
	for i := len(out) - 1; i > 0; i-- {
		j := int(buf[i%len(buf)]) % (i + 1)
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

func generateAPIKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand 不可用: " + err.Error())
	}
	return "sk_" + hex.EncodeToString(b)
}
