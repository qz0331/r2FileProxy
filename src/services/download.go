package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// HTTP 客户端：
//   - 连接超时 30s，响应头超时 60s，整体不设超时（大文件下载可能很久）；
//   - 由调用方的 context 控制任务级超时与取消。
var httpClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: 5 * time.Second,
	},
}

var (
	// ErrNotAllowed 目标地址不允许访问
	ErrNotAllowed = errors.New("目标地址不被允许访问（内网地址已拦截）")
	// ErrTooLarge 文件超过大小限制
	ErrTooLarge = errors.New("文件超过大小限制")
)

// 默认请求头，避免部分站点直接拒绝
const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"

// DownloadResult 下载结果
type DownloadResult struct {
	Filename    string
	ContentType string
	Size        int64 // -1 表示未知
	Stream      func(ctx context.Context, dst io.Writer, progress func(int64)) error
	Cleanup     func()
}

var githubRepoPattern = regexp.MustCompile(`^https?://(?:www\.)?github\.com/([^/]+)/([^/?#]+)(?:/(?:tree|blob)/([^/?#]+)(/[^?#]*)?)?/?$`)

// isGitHubRepo 判断是否为 GitHub 仓库地址
func isGitHubRepo(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	return host == "github.com" || host == "www.github.com"
}

// validateRemoteURL 仅允许 http/https，并阻断明显的内网主机名
func validateRemoteURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("URL 格式错误")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("仅支持 http/https 协议")
	}
	if u.User != nil {
		return nil, fmt.Errorf("URL 不能包含认证信息")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return nil, ErrNotAllowed
	}
	return u, nil
}

// PrepareDownload 解析目标地址，返回可流式读取的结果
//   - GitHub 仓库：下载 zip 归档
//   - 普通 URL：流式透传
func PrepareDownload(ctx context.Context, rawURL, customFilename string, maxSize int64) (*DownloadResult, error) {
	u, err := validateRemoteURL(rawURL)
	if err != nil {
		return nil, err
	}
	if isGitHubRepo(u) {
		return prepareGitHubArchive(ctx, u, customFilename, maxSize)
	}
	return prepareRegularDownload(ctx, u, customFilename, maxSize)
}

func prepareRegularDownload(ctx context.Context, u *url.URL, customFilename string, maxSize int64) (*DownloadResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("下载失败，HTTP %d", resp.StatusCode)
	}
	if maxSize > 0 && resp.ContentLength > maxSize {
		resp.Body.Close()
		return nil, ErrTooLarge
	}

	filename := customFilename
	if filename == "" {
		filename = filenameFromResponse(u, resp)
	}
	if err := SanitizeDownloadFilename(filename); err != nil {
		resp.Body.Close()
		return nil, err
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	var once sync.Once
	return &DownloadResult{
		Filename:    filename,
		ContentType: contentType,
		Size:        resp.ContentLength,
		Stream: func(ctx context.Context, dst io.Writer, progress func(int64)) error {
			defer once.Do(func() { resp.Body.Close() })
			var reader io.Reader = resp.Body
			if maxSize > 0 {
				reader = io.LimitReader(resp.Body, maxSize+1)
			}
			n, err := copyWithProgress(ctx, dst, reader, progress)
			if maxSize > 0 && n > maxSize {
				return ErrTooLarge
			}
			return err
		},
		Cleanup: func() {},
	}, nil
}

func prepareGitHubArchive(ctx context.Context, u *url.URL, customFilename string, maxSize int64) (*DownloadResult, error) {
	matches := githubRepoPattern.FindStringSubmatch(u.String())
	if len(matches) < 3 {
		return nil, fmt.Errorf("无效的 GitHub 链接")
	}
	owner := matches[1]
	repo := strings.TrimSuffix(matches[2], ".git")
	branch := ""
	if len(matches) > 3 {
		branch = matches[3]
	}

	var releases []string
	if branch != "" {
		releases = []string{branch, "main", "master"}
	} else {
		if detected := detectGitHubDefaultBranch(ctx, owner, repo); detected != "" {
			releases = append(releases, detected)
		}
		releases = append(releases, "main", "master")
	}

	var lastErr error
	seen := map[string]bool{}
	for _, b := range releases {
		if b == "" || seen[b] {
			continue
		}
		seen[b] = true

		archiveURL := fmt.Sprintf("https://codeload.github.com/%s/%s/zip/refs/heads/%s", owner, repo, url.PathEscape(b))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
		if err != nil {
			return nil, fmt.Errorf("创建请求失败: %w", err)
		}
		req.Header.Set("User-Agent", defaultUserAgent)

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("下载 GitHub 仓库失败: %w", err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("仓库 %s/%s 的分支 %s 不存在", owner, repo, b)
			continue
		}
		if maxSize > 0 && resp.ContentLength > maxSize {
			resp.Body.Close()
			return nil, ErrTooLarge
		}

		filename := customFilename
		if filename == "" {
			filename = fmt.Sprintf("%s-%s.zip", repo, b)
		}
		if !strings.HasSuffix(strings.ToLower(filename), ".zip") {
			filename += ".zip"
		}

		var once sync.Once
		return &DownloadResult{
			Filename:    filename,
			ContentType: "application/zip",
			Size:        resp.ContentLength,
			Stream: func(ctx context.Context, dst io.Writer, progress func(int64)) error {
				defer once.Do(func() { resp.Body.Close() })
				var reader io.Reader = resp.Body
				if maxSize > 0 {
					reader = io.LimitReader(resp.Body, maxSize+1)
				}
				n, err := copyWithProgress(ctx, dst, reader, progress)
				if maxSize > 0 && n > maxSize {
					return ErrTooLarge
				}
				return err
			},
			Cleanup: func() {},
		}, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("仓库不存在或无法访问")
	}
	return nil, lastErr
}

// detectGitHubDefaultBranch 通过 GitHub API 探测默认分支
func detectGitHubDefaultBranch(ctx context.Context, owner, repo string) string {
	apiCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(apiCtx, http.MethodGet,
		fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	m := regexp.MustCompile(`"default_branch"\s*:\s*"([^"]+)"`).FindSubmatch(body)
	if len(m) < 2 {
		return ""
	}
	return string(m[1])
}

// filenameFromResponse 从 Content-Disposition 或 URL 推断文件名
func filenameFromResponse(u *url.URL, resp *http.Response) string {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			if name := strings.TrimSpace(params["filename"]); name != "" {
				return name
			}
		}
		if idx := strings.Index(strings.ToLower(cd), "filename="); idx >= 0 {
			name := strings.Trim(cd[idx+len("filename="):], ` "'`)
			if name != "" {
				return name
			}
		}
	}

	name := path.Base(u.Path)
	if name == "" || name == "/" || name == "." {
		name = fmt.Sprintf("download_%d", time.Now().Unix())
	}
	if decoded, err := url.PathUnescape(name); err == nil && decoded != "" {
		name = decoded
	}
	return name
}

// SanitizeDownloadFilename 过滤危险的文件名
func SanitizeDownloadFilename(name string) error {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("无法解析文件名，请手动指定文件名")
	}
	if len([]rune(name)) > 200 {
		return fmt.Errorf("文件名过长，请手动指定")
	}
	return nil
}

// copyWithProgress 边复制边回调进度，并响应 context 取消
func copyWithProgress(ctx context.Context, dst io.Writer, src io.Reader, progress func(int64)) (int64, error) {
	buf := make([]byte, 256<<10)
	var total int64

	for {
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}

		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
			if progress != nil {
				progress(int64(n))
			}
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

// ExtractFilename 从 URL 或响应头提取文件名（兼容旧调用）
func ExtractFilename(targetURL string, resp *http.Response) string {
	u, err := url.Parse(targetURL)
	if err != nil {
		u = &url.URL{}
	}
	name := filenameFromResponse(u, resp)
	if err := SanitizeDownloadFilename(name); err != nil {
		log.Printf("文件名解析异常，使用默认值: %v", err)
		return fmt.Sprintf("download_%d", time.Now().Unix())
	}
	return name
}
