package controllers

import (
	"strings"
	"testing"
	"time"

	"r2proxy/src/models"
)

func TestValidateFilename(t *testing.T) {
	valid := []string{"a.txt", "报告 2024.pdf", "archive.tar.gz", "a_b-c.d"}
	for _, name := range valid {
		if err := validateFilename(name); err != nil {
			t.Fatalf("%q should be valid: %v", name, err)
		}
	}

	invalid := []string{"", "   ", "../etc/passwd", "a/b.txt", `a\b.txt`, "a\x00b", "line\nbreak", strings.Repeat("a", 201)}
	for _, name := range invalid {
		if err := validateFilename(name); err == nil {
			t.Fatalf("%q should be invalid", name)
		}
	}
}

func TestGenerateR2Key(t *testing.T) {
	key := generateR2Key("uploads", 7, "我的 报告.v2.PDF")
	if !strings.HasPrefix(key, "uploads/7/") {
		t.Fatalf("unexpected prefix: %s", key)
	}
	if !strings.HasSuffix(key, ".pdf") {
		t.Fatalf("extension should be lowercased: %s", key)
	}
	if strings.ContainsAny(key, " \t") {
		t.Fatalf("key should not contain spaces: %s", key)
	}

	// 危险文件名也不能产生路径穿越
	evil := generateR2Key("share", 1, "..%2f..%2fetc")
	if strings.Contains(evil, "..") {
		t.Fatalf("key contains path traversal: %s", evil)
	}

	empty := generateR2Key("", 1, ".gitignore")
	if !strings.HasPrefix(empty, "uploads/1/") {
		t.Fatalf("empty prefix should default to uploads: %s", empty)
	}
}

func TestContentDisposition(t *testing.T) {
	header := contentDisposition("报告 2024.pdf")
	if !strings.Contains(header, "filename*=UTF-8''") {
		t.Fatalf("missing RFC5987 filename: %s", header)
	}
	if strings.Contains(header, "\n") {
		t.Fatal("header must not contain newlines")
	}
	// ASCII 名不应被转换
	if h := contentDisposition("report.pdf"); !strings.Contains(h, `filename="report.pdf"`) {
		t.Fatalf("unexpected header: %s", h)
	}
}

func TestSafeNextPath(t *testing.T) {
	ok := map[string]string{
		"/settings":   "/settings",
		"/?page=2":    "/?page=2",
		"/shares?x=1": "/shares?x=1",
	}
	for in, want := range ok {
		if got := safeNextPath(in); got != want {
			t.Fatalf("safeNextPath(%q)=%q want %q", in, got, want)
		}
	}
	bad := []string{"", "https://evil.com", "//evil.com", "javascript:alert(1)", "settings"}
	for _, in := range bad {
		if got := safeNextPath(in); got != "" {
			t.Fatalf("safeNextPath(%q)=%q want empty", in, got)
		}
	}
}

func TestSanitizePrefix(t *testing.T) {
	if got := sanitizePrefix("../../etc"); got != "etc" {
		t.Fatalf("got %q", got)
	}
	if got := sanitizePrefix("ok/prefix-1"); got != "ok/prefix-1" {
		t.Fatalf("got %q", got)
	}
	if got := sanitizePrefix("中文"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestValidatePasswordComplexity(t *testing.T) {
	good := "Abcdefgh12345!@#"
	if err := validatePasswordComplexity(good); err != nil {
		t.Fatalf("should accept %q: %v", good, err)
	}
	cases := map[string]string{
		"short":      "Ab1!",
		"no-upper":   "abcdefgh12345!@#",
		"no-lower":   "ABCDEFGH12345!@#",
		"no-digit":   "Abcdefghijkl!@#",
		"no-special": "Abcdefgh12345678",
	}
	for label, pw := range cases {
		if err := validatePasswordComplexity(pw); err == nil {
			t.Fatalf("%s: %q should be rejected", label, pw)
		}
	}
}

func TestShouldCompressUpload(t *testing.T) {
	if !shouldCompressUpload("text/plain", "a.txt") {
		t.Fatal("text should compress")
	}
	if shouldCompressUpload("image/jpeg", "a.jpg") {
		t.Fatal("jpeg should not compress")
	}
	if shouldCompressUpload("application/zip", "a.zip") {
		t.Fatal("zip should not compress")
	}
	if !shouldCompressUpload("application/json", "data") {
		t.Fatal("json should compress")
	}
}

func TestEffectiveMaxFileSize(t *testing.T) {
	share := &models.ShareToken{}
	share.MaxFileSize = 100
	share.MaxTotalSize = 0
	// 全局限制在测试中未设置，应为 0（不限制），因此取单文件限制
	if got := effectiveMaxFileSize(share); got != 100 {
		t.Fatalf("got %d", got)
	}

	share.MaxTotalSize = 500
	share.UsedSize = 450
	if got := effectiveMaxFileSize(share); got != 50 {
		t.Fatalf("remaining should cap at 50, got %d", got)
	}
}

func TestShareTokenStatus(t *testing.T) {
	share := models.ShareToken{MaxUploads: 2}
	share.ExpiresAt = time.Now().Add(time.Hour)
	if share.Status() != "active" {
		t.Fatalf("status %s", share.Status())
	}
	share.UsedCount = 2
	if share.Status() != "quota_exceeded" {
		t.Fatalf("status %s", share.Status())
	}
	share.Revoked = true
	if share.Status() != "revoked" {
		t.Fatalf("status %s", share.Status())
	}
}
