package services

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"io"
	"os"
	"strings"
	"testing"
)

func TestSpoolCompressSmaller(t *testing.T) {
	src := strings.NewReader(strings.Repeat("r2proxy-compress-me ", 2000))
	sp, err := newSpoolFile()
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Cleanup()

	var progress int64
	if err := sp.Fill(src, true, 0, func(n int64) { progress += n }); err != nil {
		t.Fatal(err)
	}
	if progress != sp.RawSize {
		t.Fatalf("progress=%d raw=%d", progress, sp.RawSize)
	}
	if !sp.Compressed() {
		t.Fatal("expected compressed")
	}
	if sp.UploadSize() >= sp.RawSize {
		t.Fatalf("compressed size %d >= raw %d", sp.UploadSize(), sp.RawSize)
	}

	f, err := sp.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	head := make([]byte, 2)
	if _, err := io.ReadFull(f, head); err != nil {
		t.Fatal(err)
	}
	if head[0] != 0x1f || head[1] != 0x8b {
		t.Fatalf("expected gzip magic, got %x", head)
	}
}

func TestSpoolFallsBackForRandomData(t *testing.T) {
	data := make([]byte, 200000)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	sp, err := newSpoolFile()
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Cleanup()

	if err := sp.Fill(bytes.NewReader(data), true, 0, nil); err != nil {
		t.Fatal(err)
	}
	if sp.Compressed() {
		t.Fatalf("random data should not stay compressed (raw=%d gz=%d)", sp.RawSize, sp.GzSize)
	}
	if sp.UploadSize() != int64(len(data)) {
		t.Fatalf("upload size = %d, want %d", sp.UploadSize(), len(data))
	}
}

func TestSpoolRespectsMaxSize(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 5000)
	sp, err := newSpoolFile()
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Cleanup()

	err = sp.Fill(bytes.NewReader(data), false, 1000, nil)
	if err == nil {
		t.Fatal("expected size limit error")
	}
}

func TestSpoolSectionAndCleanup(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789"), 1000) // 10KB
	sp, err := newSpoolFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := sp.Fill(bytes.NewReader(data), false, 0, nil); err != nil {
		t.Fatal(err)
	}

	section, err := sp.Section(100, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(section, data[100:150]) {
		t.Fatal("section mismatch")
	}

	rawPath := sp.rawPath
	sp.Cleanup()
	if _, err := os.Stat(rawPath); !os.IsNotExist(err) {
		t.Fatal("temp file should be removed")
	}
}

func TestSanitizeDownloadFilename(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		want string
	}{
		{"../../etc/passwd", true, "passwd"},
		{`C:\Windows\evil.exe`, true, "evil.exe"},
		{"", false, ""},
		{"..", false, ""},
	}
	for _, c := range cases {
		err := SanitizeDownloadFilename(c.in)
		if c.ok && err != nil {
			t.Fatalf("%q: unexpected error %v", c.in, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("%q: expected error", c.in)
		}
	}
}

func TestValidateRemoteURL(t *testing.T) {
	if _, err := validateRemoteURL("ftp://example.com/a"); err == nil {
		t.Fatal("ftp should be rejected")
	}
	if _, err := validateRemoteURL("http://localhost:8080/a"); err == nil {
		t.Fatal("localhost should be rejected")
	}
	if _, err := validateRemoteURL("http://user:pass@example.com/a"); err == nil {
		t.Fatal("credentials should be rejected")
	}
	if _, err := validateRemoteURL("https://example.com/a.zip"); err != nil {
		t.Fatalf("valid url rejected: %v", err)
	}
}

func TestGitHubRepoPattern(t *testing.T) {
	cases := map[string]bool{
		"https://github.com/owner/repo":             true,
		"https://github.com/owner/repo.git":         true,
		"https://github.com/owner/repo/tree/dev":    true,
		"https://github.com/owner/repo/blob/main/x": true,
	}
	for raw := range cases {
		u, err := validateRemoteURL(raw)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if !isGitHubRepo(u) {
			t.Fatalf("%s: should be github", raw)
		}
		if m := githubRepoPattern.FindStringSubmatch(u.String()); len(m) < 3 {
			t.Fatalf("%s: pattern mismatch", raw)
		}
	}
}

func TestSpoolCompressedCanBeDecompressed(t *testing.T) {
	payload := strings.Repeat("hello r2proxy\n", 500)
	sp, err := newSpoolFile()
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Cleanup()

	if err := sp.Fill(strings.NewReader(payload), true, 0, nil); err != nil {
		t.Fatal(err)
	}
	if !sp.Compressed() {
		t.Fatal("expected compression for repetitive text")
	}

	f, err := sp.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != payload {
		t.Fatal("decompressed payload mismatch")
	}
}
