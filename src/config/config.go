package config

import (
	"log"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	ServerPort     string
	ServerSecret   string
	SecurityEntry  string
	SecureCookie   bool
	TrustedProxies []string
	PublicBaseURL  string

	MaxDownloadSize int64 // 单个 URL 下载任务允许的最大字节数
	MaxUploadSize   int64 // 分享页代理上传允许的最大字节数

	R2AccountID   string
	R2AccessKeyID string
	R2SecretKey   string
	R2BucketName  string
	R2PublicURL   string
	DatabaseURL   string
}

// 安全入口允许的字符集
var securityEntryPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// 保留前缀，与现有路由冲突
var reservedEntryPrefixes = []string{"api", "static", "s", "healthz"}

var AppConfig *Config

func Load() {
	// 加载 .env 文件（如果存在）
	if err := godotenv.Load(); err != nil {
		log.Println("警告: 未找到 .env 文件，使用系统环境变量")
	}

	AppConfig = &Config{
		ServerPort:      getEnv("SERVER_PORT", "8080"),
		ServerSecret:    getEnv("SERVER_SECRET", "default-secret-change-me"),
		SecurityEntry:   strings.Trim(getEnv("SECURITY_ENTRY", ""), "/"),
		SecureCookie:    getEnvBool("SECURE_COOKIE", false),
		TrustedProxies:  getEnvListDefault("TRUSTED_PROXIES", []string{"127.0.0.1", "::1"}),
		PublicBaseURL:   strings.TrimRight(getEnv("PUBLIC_BASE_URL", ""), "/"),
		MaxDownloadSize: getEnvSize("MAX_DOWNLOAD_SIZE", 2<<30), // 2 GiB
		MaxUploadSize:   getEnvSize("MAX_UPLOAD_SIZE", 2<<30),   // 2 GiB
		R2AccountID:     getEnv("R2_ACCOUNT_ID", ""),
		R2AccessKeyID:   getEnv("R2_ACCESS_KEY_ID", ""),
		R2SecretKey:     getEnv("R2_SECRET_ACCESS_KEY", ""),
		R2BucketName:    getEnv("R2_BUCKET_NAME", ""),
		R2PublicURL:     getEnv("R2_PUBLIC_URL", ""),
		DatabaseURL:     getEnv("DATABASE_URL", "r2proxy.db"),
	}

	validateSecurityEntry(AppConfig.SecurityEntry)
	validateBaseURL(AppConfig.PublicBaseURL)

	if AppConfig.SecurityEntry == "" {
		log.Println("安全入口: 未启用")
	} else {
		log.Printf("安全入口: 已启用(/%s)", AppConfig.SecurityEntry)
	}

	if AppConfig.ServerSecret == "default-secret-change-me" || AppConfig.ServerSecret == "" {
		log.Println("警告: SERVER_SECRET 使用默认值，请在 .env 中设置随机密钥")
	}

	// 调试：打印配置（隐藏敏感信息）
	log.Printf("配置加载完成: R2_ACCOUNT_ID=%s, R2_BUCKET_NAME=%s, SECURE_COOKIE=%v",
		AppConfig.R2AccountID, AppConfig.R2BucketName, AppConfig.SecureCookie)
}

// validateSecurityEntry 校验安全入口字符串，非法时直接终止启动
func validateSecurityEntry(entry string) {
	if entry == "" {
		return
	}
	if !securityEntryPattern.MatchString(entry) {
		log.Fatalf("安全入口格式非法: 仅允许字母、数字、下划线和连字符，长度 1-64")
	}
	for _, reserved := range reservedEntryPrefixes {
		if strings.EqualFold(entry, reserved) {
			log.Fatalf("安全入口不能使用保留前缀: %s", reserved)
		}
	}
}

// validateBaseURL 校验 PUBLIC_BASE_URL，非法时忽略并通过请求推断
func validateBaseURL(raw string) {
	if raw == "" {
		return
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		log.Printf("警告: PUBLIC_BASE_URL=%q 不是合法的 http(s) 地址，已忽略", raw)
		AppConfig.PublicBaseURL = ""
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultValue
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		log.Printf("警告: %s=%q 不是合法布尔值，使用默认值 %v", key, v, defaultValue)
		return defaultValue
	}
}

// getEnvList 解析逗号分隔的列表，支持 * 表示全部
func getEnvList(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	if raw == "*" {
		return []string{"0.0.0.0/0", "::/0"}
	}
	var out []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// getEnvListDefault 同 getEnvList，带默认值
func getEnvListDefault(key string, defaults []string) []string {
	if _, ok := os.LookupEnv(key); !ok {
		return defaults
	}
	return getEnvList(key)
}

// getEnvSize 解析带单位的字节数，支持 1024、10MB、2G 等写法
func getEnvSize(key string, defaultValue int64) int64 {
	raw := strings.TrimSpace(strings.ToUpper(os.Getenv(key)))
	if raw == "" {
		return defaultValue
	}
	multiplier := int64(1)
	for _, suffix := range []struct {
		s string
		m int64
	}{
		{"TB", 1 << 40}, {"T", 1 << 40},
		{"GB", 1 << 30}, {"G", 1 << 30},
		{"MB", 1 << 20}, {"M", 1 << 20},
		{"KB", 1 << 10}, {"K", 1 << 10},
		{"B", 1},
	} {
		if strings.HasSuffix(raw, suffix.s) {
			multiplier = suffix.m
			raw = strings.TrimSpace(strings.TrimSuffix(raw, suffix.s))
			break
		}
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		log.Printf("警告: %s 不是合法大小，使用默认值 %d", key, defaultValue)
		return defaultValue
	}
	return n * multiplier
}
