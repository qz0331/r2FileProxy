package services

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// spoolFile 将上传流先落盘到临时文件：
//   - 大文件分片上传时作为可重复读取的数据源（支持重试）；
//   - 需要压缩时把原始数据 gzip 到第二个临时文件，只有确实变小时才采用压缩版本。
type spoolFile struct {
	rawPath string
	gzPath  string
	useGzip bool
	RawSize int64
	GzSize  int64
}

func newSpoolFile() (*spoolFile, error) {
	dir := os.TempDir()
	raw, err := os.CreateTemp(dir, "r2proxy-raw-*")
	if err != nil {
		return nil, fmt.Errorf("创建临时文件失败: %w", err)
	}
	sp := &spoolFile{rawPath: raw.Name()}
	if err := raw.Close(); err != nil {
		sp.Cleanup()
		return nil, err
	}
	return sp, nil
}

// Fill 从 src 读取数据写入临时文件，可选同时压缩。
// maxSize <= 0 表示不限制；progress 收到的是原始字节增量。
func (s *spoolFile) Fill(src io.Reader, compress bool, maxSize int64, progress func(int64)) error {
	raw, err := os.Create(s.rawPath)
	if err != nil {
		return fmt.Errorf("打开临时文件失败: %w", err)
	}

	counter := &progressWriter{w: raw, progress: progress}
	var written int64

	if maxSize > 0 {
		written, err = io.Copy(counter, io.LimitReader(src, maxSize+1))
	} else {
		written, err = io.Copy(counter, src)
	}
	closeErr := raw.Close()
	if err != nil {
		return fmt.Errorf("读取上传数据失败: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("写入临时文件失败: %w", closeErr)
	}
	if maxSize > 0 && written > maxSize {
		return fmt.Errorf("文件超过大小限制（最大 %s）", humanSize(maxSize))
	}
	s.RawSize = written

	if !compress || written == 0 {
		return nil
	}

	// 压缩到单独的临时文件
	gz, err := os.CreateTemp(filepath.Dir(s.rawPath), "r2proxy-gz-*")
	if err != nil {
		return fmt.Errorf("创建压缩临时文件失败: %w", err)
	}
	s.gzPath = gz.Name()

	in, err := os.Open(s.rawPath)
	if err != nil {
		gz.Close()
		return err
	}
	defer in.Close()

	zw, err := gzip.NewWriterLevel(gz, gzip.BestSpeed)
	if err != nil {
		gz.Close()
		return err
	}

	if _, err = io.Copy(zw, in); err != nil {
		gz.Close()
		return fmt.Errorf("压缩文件失败: %w", err)
	}
	if cerr := zw.Close(); cerr != nil {
		gz.Close()
		return fmt.Errorf("压缩文件失败: %w", cerr)
	}
	if cerr := gz.Close(); cerr != nil {
		return fmt.Errorf("写入压缩文件失败: %w", cerr)
	}

	// gzip.Writer.Write 返回的是未压缩字节数，实际大小需从文件读取
	info, statErr := os.Stat(s.gzPath)
	if statErr != nil {
		return fmt.Errorf("读取压缩文件信息失败: %w", statErr)
	}
	s.GzSize = info.Size()

	// 压缩后反而更大则丢弃压缩文件
	if s.GzSize >= s.RawSize {
		os.Remove(s.gzPath)
		s.gzPath = ""
		s.GzSize = 0
		return nil
	}
	s.useGzip = true
	return nil
}

// Compressed 是否采用 gzip 压缩版本
func (s *spoolFile) Compressed() bool { return s.useGzip }

// UploadSize 实际需要上传的字节数
func (s *spoolFile) UploadSize() int64 {
	if s.useGzip {
		return s.GzSize
	}
	return s.RawSize
}

// Path 返回实际要上传的临时文件路径
func (s *spoolFile) Path() string {
	if s.useGzip {
		return s.gzPath
	}
	return s.rawPath
}

// Open 打开上传数据文件
func (s *spoolFile) Open() (*os.File, error) {
	return os.Open(s.Path())
}

// Section 读取 [offset, offset+length) 区间（用于分片上传）
func (s *spoolFile) Section(offset, length int64) ([]byte, error) {
	f, err := os.Open(s.Path())
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := make([]byte, length)
	if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
		return nil, err
	}
	return buf, nil
}

// Cleanup 删除临时文件
func (s *spoolFile) Cleanup() {
	if s.rawPath != "" {
		os.Remove(s.rawPath)
	}
	if s.gzPath != "" {
		os.Remove(s.gzPath)
	}
}

// progressWriter 统计写入字节数并回调增量
type progressWriter struct {
	w        io.Writer
	progress func(int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	if n > 0 && p.progress != nil {
		p.progress(int64(n))
	}
	return n, err
}

// humanSize 与前端一致的易读大小
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 4 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
