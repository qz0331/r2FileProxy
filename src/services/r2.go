package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	appconfig "r2proxy/src/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// R2Client R2 客户端
type R2Client struct {
	client    *s3.Client
	bucket    string
	publicURL string
}

// R2Service R2 服务实例
var R2Service *R2Client

// multipartPartSize 分片大小。除最后一片外，R2 要求每片 >= 5MiB。
const multipartPartSize = 16 << 20

// multipartConcurrency 分片上传并发数
const multipartConcurrency = 4

// InitR2 初始化 R2 客户端
func InitR2() error {
	cfg, err := awsconfig.LoadDefaultConfig(context.TODO(),
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			appconfig.AppConfig.R2AccessKeyID,
			appconfig.AppConfig.R2SecretKey,
			"",
		)),
	)
	if err != nil {
		return fmt.Errorf("加载 AWS 配置失败: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(fmt.Sprintf(
			"https://%s.r2.cloudflarestorage.com",
			appconfig.AppConfig.R2AccountID,
		))
	})

	R2Service = &R2Client{
		client:    client,
		bucket:    appconfig.AppConfig.R2BucketName,
		publicURL: strings.TrimRight(appconfig.AppConfig.R2PublicURL, "/"),
	}

	return nil
}

// GenerateUploadURL 生成上传签名 URL
func (r *R2Client) GenerateUploadURL(ctx context.Context, key string, expiresIn time.Duration) (string, error) {
	return r.GenerateUploadURLWithContentType(ctx, key, expiresIn, "")
}

// GenerateUploadURLWithContentType 生成带 Content-Type 的上传签名 URL
func (r *R2Client) GenerateUploadURLWithContentType(ctx context.Context, key string, expiresIn time.Duration, contentType string) (string, error) {
	if r == nil || r.client == nil {
		return "", fmt.Errorf("R2 客户端未初始化，请检查 R2 配置")
	}

	presignClient := s3.NewPresignClient(r.client)

	input := &s3.PutObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	}

	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}

	req, err := presignClient.PresignPutObject(ctx, input, func(opts *s3.PresignOptions) {
		opts.Expires = expiresIn
	})
	if err != nil {
		return "", fmt.Errorf("生成上传签名失败: %w", err)
	}

	return req.URL, nil
}

// GenerateDownloadURL 生成下载签名 URL
func (r *R2Client) GenerateDownloadURL(ctx context.Context, key string, expiresIn time.Duration) (string, error) {
	return r.GenerateDownloadURLWithDisposition(ctx, key, expiresIn, "")
}

// GenerateDownloadURLWithDisposition 生成带 Content-Disposition 的下载签名 URL
func (r *R2Client) GenerateDownloadURLWithDisposition(ctx context.Context, key string, expiresIn time.Duration, disposition string) (string, error) {
	if r == nil || r.client == nil {
		return "", fmt.Errorf("R2 客户端未初始化，请检查 R2 配置")
	}

	presignClient := s3.NewPresignClient(r.client)

	input := &s3.GetObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	}
	if disposition != "" {
		input.ResponseContentDisposition = aws.String(disposition)
	}

	req, err := presignClient.PresignGetObject(ctx, input, func(opts *s3.PresignOptions) {
		opts.Expires = expiresIn
	})
	if err != nil {
		return "", fmt.Errorf("生成下载签名失败: %w", err)
	}

	return req.URL, nil
}

// PublicURL 返回对象的公开访问地址（未配置 R2_PUBLIC_URL 时返回空串）
func (r *R2Client) PublicURL(key string) string {
	if r == nil || r.publicURL == "" {
		return ""
	}
	return r.publicURL + "/" + key
}

// DeleteObject 删除 R2 对象
func (r *R2Client) DeleteObject(ctx context.Context, key string) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("R2 客户端未初始化，请检查 R2 配置")
	}
	_, err := r.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("删除对象失败: %w", err)
	}
	return nil
}

// HeadObject 获取文件元数据
func (r *R2Client) HeadObject(ctx context.Context, key string) (*s3.HeadObjectOutput, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("R2 客户端未初始化，请检查 R2 配置")
	}
	return r.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	})
}

// ObjectExists 检查对象是否存在
func (r *R2Client) ObjectExists(ctx context.Context, key string) bool {
	_, err := r.HeadObject(ctx, key)
	return err == nil
}

// PutObject 直接上传对象到 R2
func (r *R2Client) PutObject(ctx context.Context, key string, body []byte, contentType string) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("R2 客户端未初始化，请检查 R2 配置")
	}

	input := &s3.PutObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(body),
	}

	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}

	_, err := r.client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("上传对象失败: %w", err)
	}

	return nil
}

// StreamResult 流式上传结果
type StreamResult struct {
	StoredSize      int64  `json:"stored_size"` // R2 中实际占用字节数
	RawSize         int64  `json:"raw_size"`    // 原始字节数
	ETag            string `json:"etag"`
	ContentEncoding string `json:"content_encoding"` // 空或 gzip
	Compressed      bool   `json:"compressed"`
}

// PutObjectStream 将输入流写入 R2。小于 16MiB 使用单次 PUT，否则使用分片上传：
//   - 输入流先写入临时文件，之后作为分片上传的重试源，内存占用恒定；
//   - compress=true 时先 gzip 压缩（压缩后未变小则自动回退为原始数据）；
//   - progress(n) 用于累计已接收的原始字节数，便于前端显示进度。
func (r *R2Client) PutObjectStream(
	ctx context.Context,
	key string,
	src io.Reader,
	contentType string,
	compress bool,
	maxSize int64,
	progress func(delta int64),
) (*StreamResult, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("R2 客户端未初始化，请检查 R2 配置")
	}

	spool, err := newSpoolFile()
	if err != nil {
		return nil, err
	}
	defer spool.Cleanup()

	if err := spool.Fill(src, compress, maxSize, progress); err != nil {
		return nil, err
	}
	if spool.RawSize == 0 {
		return nil, fmt.Errorf("文件内容为空")
	}

	result := &StreamResult{RawSize: spool.RawSize}

	if spool.Compressed() {
		result.ContentEncoding = "gzip"
		result.Compressed = true
	}

	result.StoredSize = spool.UploadSize()

	if result.StoredSize <= multipartPartSize {
		etag, err := r.putSingle(ctx, key, spool, contentType, result.ContentEncoding)
		if err != nil {
			return nil, err
		}
		result.ETag = etag
		return result, nil
	}

	etag, err := r.putMultipart(ctx, key, spool, contentType, result.ContentEncoding)
	if err != nil {
		return nil, err
	}
	result.ETag = etag
	return result, nil
}

func (r *R2Client) putSingle(ctx context.Context, key string, spool *spoolFile, contentType, encoding string) (string, error) {
	f, err := spool.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()

	input := &s3.PutObjectInput{
		Bucket:        aws.String(r.bucket),
		Key:           aws.String(key),
		Body:          f,
		ContentLength: aws.Int64(spool.UploadSize()),
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	if encoding != "" {
		input.ContentEncoding = aws.String(encoding)
	}

	out, err := r.client.PutObject(ctx, input)
	if err != nil {
		return "", fmt.Errorf("上传对象失败: %w", err)
	}
	return aws.ToString(out.ETag), nil
}

func (r *R2Client) putMultipart(ctx context.Context, key string, spool *spoolFile, contentType, encoding string) (string, error) {
	createInput := &s3.CreateMultipartUploadInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	}
	if contentType != "" {
		createInput.ContentType = aws.String(contentType)
	}
	if encoding != "" {
		createInput.ContentEncoding = aws.String(encoding)
	}

	createOut, err := r.client.CreateMultipartUpload(ctx, createInput)
	if err != nil {
		return "", fmt.Errorf("创建分片上传失败: %w", err)
	}
	uploadID := aws.ToString(createOut.UploadId)

	abort := func() {
		_, _ = r.client.AbortMultipartUpload(context.Background(), &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(r.bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
		})
	}

	total := spool.UploadSize()
	partCount := int((total + multipartPartSize - 1) / multipartPartSize)
	parts := make([]types.CompletedPart, partCount)

	type partResult struct {
		index int
		etag  string
		err   error
	}

	sem := make(chan struct{}, multipartConcurrency)
	results := make(chan partResult, partCount)

	var wg sync.WaitGroup
	for i := 0; i < partCount; i++ {
		start := int64(i) * multipartPartSize
		size := int64(multipartPartSize)
		if start+size > total {
			size = total - start
		}

		wg.Add(1)
		go func(index int, offset, length int64) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results <- partResult{index: index, err: ctx.Err()}
				return
			}

			section, err := spool.Section(offset, length)
			if err != nil {
				results <- partResult{index: index, err: err}
				return
			}

			out, err := r.client.UploadPart(ctx, &s3.UploadPartInput{
				Bucket:        aws.String(r.bucket),
				Key:           aws.String(key),
				UploadId:      aws.String(uploadID),
				PartNumber:    aws.Int32(int32(index + 1)),
				Body:          bytes.NewReader(section),
				ContentLength: aws.Int64(length),
			})
			if err != nil {
				results <- partResult{index: index, err: fmt.Errorf("上传分片 %d 失败: %w", index+1, err)}
				return
			}
			results <- partResult{index: index, etag: aws.ToString(out.ETag)}
		}(i, start, size)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for res := range results {
		if res.err != nil {
			abort()
			return "", res.err
		}
		parts[res.index] = types.CompletedPart{
			ETag:       aws.String(res.etag),
			PartNumber: aws.Int32(int32(res.index + 1)),
		}
	}

	completeOut, err := r.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(r.bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: parts,
		},
	})
	if err != nil {
		abort()
		return "", fmt.Errorf("完成分片上传失败: %w", err)
	}

	return aws.ToString(completeOut.ETag), nil
}
