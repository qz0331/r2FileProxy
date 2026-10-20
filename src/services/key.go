package services

import (
	"crypto/rand"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var unsafeKeyChars = regexp.MustCompile(`[^\p{Han}\p{L}\p{N}_.\-]`)

// generateKey 生成 R2 对象键，格式: {prefix}/{userId}/{baseName}_{yyMMddHHmmss}{rand}{ext}
//
// 该函数由 services 包内部使用，controllers 中同名逻辑（generateR2Key）与其保持一致。
func generateKey(prefix string, userID uint, filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if len(ext) > 16 || !regexp.MustCompile(`^\.[A-Za-z0-9]+$`).MatchString(ext) {
		ext = ""
	}
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	base = unsafeKeyChars.ReplaceAllString(base, "")
	base = strings.Trim(base, "._-")
	if base == "" {
		base = "file"
	}
	if runes := []rune(base); len(runes) > 80 {
		base = string(runes[:80])
	}
	if prefix == "" {
		prefix = "uploads"
	}
	return fmt.Sprintf("%s/%d/%s_%s%s%s",
		strings.Trim(prefix, "/"), userID, base,
		time.Now().Format("060102150405"), randomKeySuffix(4), ext)
}

func randomKeySuffix(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "0000"
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[int(buf[i])%len(chars)]
	}
	return string(b)
}

func lowerExt(name string) string {
	return strings.ToLower(filepath.Ext(name))
}

func lowerString(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
