package services

import (
	"context"
	"fmt"
	"io"
	"log"
	"r2proxy/src/config"
	"r2proxy/src/database"
	"r2proxy/src/models"
	"sync"
	"time"

	"gorm.io/gorm"
)

// 任务并发与超时
const (
	maxConcurrentTasks = 3
	taskTimeout        = 6 * time.Hour
	progressFlush      = time.Second
)

// taskManager 管理后台 URL 下载任务的并发、进度与取消
type taskManager struct {
	mu      sync.Mutex
	sem     chan struct{}
	running map[uint]context.CancelFunc
}

var manager = &taskManager{
	sem:     make(chan struct{}, maxConcurrentTasks),
	running: make(map[uint]context.CancelFunc),
}

// SubmitTask 领取任务并异步执行
func SubmitTask(taskID uint) {
	ctx, cancel := context.WithTimeout(context.Background(), taskTimeout)

	manager.mu.Lock()
	manager.running[taskID] = cancel
	manager.mu.Unlock()

	go func() {
		defer func() {
			manager.mu.Lock()
			delete(manager.running, taskID)
			manager.mu.Unlock()
			cancel()

			if r := recover(); r != nil {
				log.Printf("下载任务 %d panic: %v", taskID, r)
				markTaskFailed(taskID, "任务异常终止")
			}
		}()

		select {
		case manager.sem <- struct{}{}:
			defer func() { <-manager.sem }()
		case <-ctx.Done():
			markTaskFailed(taskID, "任务已取消")
			return
		}

		runTask(ctx, taskID)
	}()
}

// CancelTask 取消正在执行的任务
func CancelTask(taskID uint) bool {
	manager.mu.Lock()
	cancel, ok := manager.running[taskID]
	manager.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// IsRunning 任务是否正在执行
func IsRunning(taskID uint) bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	_, ok := manager.running[taskID]
	return ok
}

// RunningCount 正在执行的任务数
func RunningCount() int {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return len(manager.running)
}

func runTask(ctx context.Context, taskID uint) {
	var task models.DownloadTask
	if err := database.DB.First(&task, taskID).Error; err != nil {
		return
	}
	if task.Status == models.TaskStatusCancelled {
		return
	}

	task.Status = models.TaskStatusDownloading
	task.Error = ""
	database.DB.Save(&task)

	result, err := PrepareDownload(ctx, task.URL, task.CustomName, config.AppConfig.MaxDownloadSize)
	if err != nil {
		finishTask(&task, err)
		return
	}
	defer result.Cleanup()

	task.Filename = result.Filename
	task.ContentType = result.ContentType
	task.Size = result.Size
	if task.Size < 0 {
		task.Size = 0
	}
	database.DB.Model(&task).Updates(map[string]any{
		"filename":     result.Filename,
		"content_type": result.ContentType,
		"size":         result.Size,
	})

	key := GenerateObjectKey("downloads", task.UserID, result.Filename)
	progress := newTaskProgress(task.ID)

	// result.Stream 负责从网络拉取数据；PutObjectStream 负责压缩/落盘/分片上传
	streamResult, err := R2Service.PutObjectStream(
		ctx,
		key,
		pipeReader(func(w io.Writer) error {
			return result.Stream(ctx, w, progress.Add)
		}),
		result.ContentType,
		shouldCompress(result.ContentType, result.Filename),
		config.AppConfig.MaxDownloadSize,
		progress.Add,
	)
	if err != nil {
		finishTask(&task, err)
		return
	}

	file := models.File{
		UserID:           task.UserID,
		TemporaryTokenID: task.TempTokenID,
		ShareTokenID:     task.ShareTokenID,
		Key:              key,
		Filename:         result.Filename,
		Remark:           task.Remark,
		Size:             streamResult.RawSize,
		StoredSize:       streamResult.StoredSize,
		ContentType:      result.ContentType,
		ContentEncoding:  streamResult.ContentEncoding,
		ETag:             streamResult.ETag,
		Status:           models.FileStatusConfirmed,
	}
	if file.Remark == "" {
		file.Remark = result.Filename
	}

	if err := database.DB.Create(&file).Error; err != nil {
		finishTask(&task, fmt.Errorf("创建文件记录失败"))
		_ = R2Service.DeleteObject(context.Background(), key)
		return
	}

	// 取消请求可能在下载完成后到达，此时保留文件但标记完成
	task.Status = models.TaskStatusCompleted
	task.FileID = &file.ID
	task.Size = streamResult.RawSize
	task.Downloaded = streamResult.RawSize
	task.Error = ""
	database.DB.Save(&task)

	if task.ShareTokenID != nil {
		database.DB.Model(&models.ShareToken{}).Where("id = ?", *task.ShareTokenID).
			UpdateColumn("used_size", gorm.Expr("used_size + ?", streamResult.RawSize))
	}
	if task.TempTokenID != nil {
		database.DB.Model(&models.TemporaryToken{}).Where("id = ?", *task.TempTokenID).
			UpdateColumn("used_count", gorm.Expr("used_count + 1"))
	}
}

// pipeReader 把“向 io.Writer 写入”的函数适配成 io.Reader
func pipeReader(fn func(w io.Writer) error) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		err := fn(pw)
		pw.CloseWithError(err)
	}()
	return pr
}

func finishTask(task *models.DownloadTask, err error) {
	if err == nil {
		return
	}

	msg := err.Error()
	switch {
	case err == context.Canceled:
		msg = "任务已取消"
		task.Status = models.TaskStatusCancelled
	case err == context.DeadlineExceeded:
		msg = "下载超时（超过 6 小时）"
		task.Status = models.TaskStatusFailed
	case err == ErrTooLarge:
		msg = "文件超过大小限制"
		task.Status = models.TaskStatusFailed
	default:
		task.Status = models.TaskStatusFailed
	}

	task.Error = truncate(msg, 500)
	database.DB.Save(task)
	log.Printf("下载任务 %d 失败: %s", task.ID, msg)
}

// markTaskFailed 将任务标记为失败（仅在未完成时生效）
func markTaskFailed(taskID uint, message string) {
	database.DB.Model(&models.DownloadTask{}).
		Where("id = ? AND status IN ?", taskID, []string{models.TaskStatusPending, models.TaskStatusDownloading}).
		Updates(map[string]any{
			"status": models.TaskStatusFailed,
			"error":  message,
		})
}

// taskProgress 按秒级频率把下载进度写库
type taskProgress struct {
	taskID   uint
	mu       sync.Mutex
	total    int64
	lastSync time.Time
}

func newTaskProgress(taskID uint) *taskProgress {
	return &taskProgress{taskID: taskID, lastSync: time.Now()}
}

// Add 累计字节数，并按秒写库
func (p *taskProgress) Add(n int64) {
	p.mu.Lock()
	p.total += n
	downloaded := p.total
	flush := time.Since(p.lastSync) > progressFlush
	if flush {
		p.lastSync = time.Now()
	}
	p.mu.Unlock()

	if flush {
		database.DB.Model(&models.DownloadTask{}).Where("id = ?", p.taskID).
			UpdateColumn("downloaded", downloaded)
	}
}

// GenerateObjectKey 生成 R2 对象键（供下载任务使用）
func GenerateObjectKey(prefix string, userID uint, filename string) string {
	return generateKey(prefix, userID, filename)
}

// shouldCompress 判断是否需要 gzip（文本类才压缩，已压缩格式跳过）
func shouldCompress(contentType, filename string) bool {
	switch lowerExt(filename) {
	case ".gz", ".tgz", ".zip", ".7z", ".rar", ".bz2", ".xz", ".zst",
		".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".heic",
		".mp3", ".mp4", ".m4a", ".mkv", ".mov", ".avi", ".flv", ".webm",
		".woff", ".woff2", ".ttf", ".otf", ".eot",
		".pdf", ".docx", ".xlsx", ".pptx", ".apk", ".ipa", ".exe", ".dmg", ".iso":
		return false
	}

	ct := lowerString(contentType)
	switch {
	case containsAny(ct, "text/", "application/json", "application/javascript",
		"application/xml", "application/x-httpd-php", "application/sql",
		"image/svg", "application/x-sh"):
		return true
	}
	return false
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
