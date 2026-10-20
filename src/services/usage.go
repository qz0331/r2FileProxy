package services

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// BucketUsage R2 桶的实时用量（来自 ListObjectsV2，与 Cloudflare 控制台口径一致）
type BucketUsage struct {
	Bytes     int64            // 所有对象 ContentLength 之和
	Objects   int64            // 对象数量
	FetchedAt time.Time        // 统计完成时间
	Err       error            // 统计失败原因
	Keys      map[string]int64 // key -> size，用于区分“本服务记录内/外”的对象
}

// bucketUsageCache 缓存统计结果，避免每次打开首页都全量扫描。
type bucketUsageCache struct {
	mu      sync.Mutex
	usage   BucketUsage
	loading bool
}

var bucketUsage = &bucketUsageCache{}

var (
	errR2NotInit    = errors.New("R2 客户端未初始化")
	errUsageTimeout = errors.New("存储用量统计超时，请稍后刷新")
)

// bucketUsageTTL 统计结果缓存时间
const bucketUsageTTL = 2 * time.Minute

// BucketUsage 获取桶的实时用量：
//   - 通过 ListObjectsV2 遍历所有对象，累加真实的 ContentLength（Cloudflare 计费口径）；
//   - 直接问 R2，因此能反映手工上传、其他工具写入、以及不在本服务数据库中的对象；
//   - 结果缓存 2 分钟，过期后由后台异步刷新；首次调用同步统计。
func (r *R2Client) BucketUsage(ctx context.Context) BucketUsage {
	if r == nil || r.client == nil {
		return BucketUsage{Err: errR2NotInit}
	}

	bucketUsage.mu.Lock()
	cached := bucketUsage.usage
	fresh := !cached.FetchedAt.IsZero() && time.Since(cached.FetchedAt) < bucketUsageTTL
	if fresh {
		bucketUsage.mu.Unlock()
		return cached
	}

	if !cached.FetchedAt.IsZero() {
		// 已有旧值：本次先返回旧值，后台刷新
		if !bucketUsage.loading {
			bucketUsage.loading = true
			go r.refreshUsage()
		}
		bucketUsage.mu.Unlock()
		return cached
	}

	// 首次调用：同步统计
	if bucketUsage.loading {
		// 另一个请求正在统计，短暂等待后返回
		bucketUsage.mu.Unlock()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
			bucketUsage.mu.Lock()
			u := bucketUsage.usage
			loading := bucketUsage.loading
			bucketUsage.mu.Unlock()
			if !loading || !u.FetchedAt.IsZero() {
				return u
			}
		}
		return BucketUsage{Err: errUsageTimeout}
	}
	bucketUsage.loading = true
	bucketUsage.mu.Unlock()

	r.refreshUsage()

	bucketUsage.mu.Lock()
	defer bucketUsage.mu.Unlock()
	return bucketUsage.usage
}

// refreshUsage 遍历桶内所有对象并更新缓存
func (r *R2Client) refreshUsage() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	usage := BucketUsage{Keys: make(map[string]int64)}
	var token *string

	for {
		out, err := r.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(r.bucket),
			ContinuationToken: token,
			MaxKeys:           aws.Int32(1000),
		})
		if err != nil {
			usage.Err = err
			usage.FetchedAt = time.Now()
			bucketUsage.mu.Lock()
			bucketUsage.usage = usage
			bucketUsage.loading = false
			bucketUsage.mu.Unlock()
			return
		}

		for _, obj := range out.Contents {
			key := aws.ToString(obj.Key)
			size := aws.ToInt64(obj.Size)
			usage.Objects++
			usage.Bytes += size
			usage.Keys[key] = size
		}

		if aws.ToBool(out.IsTruncated) && out.NextContinuationToken != nil {
			token = out.NextContinuationToken
			continue
		}
		break
	}

	usage.FetchedAt = time.Now()
	bucketUsage.mu.Lock()
	bucketUsage.usage = usage
	bucketUsage.loading = false
	bucketUsage.mu.Unlock()
}

// UsageSnapshot 返回当前缓存的用量快照（不触发统计）
func (r *R2Client) UsageSnapshot() BucketUsage {
	bucketUsage.mu.Lock()
	defer bucketUsage.mu.Unlock()
	return bucketUsage.usage
}

// UsageStats 桶用量与本地记录的差集结果
type UsageStats struct {
	BucketBytes   int64            `json:"bucket_bytes"`
	BucketObjects int64            `json:"bucket_objects"`
	FetchedAt     string           `json:"fetched_at"`
	Error         string           `json:"error,omitempty"`
	ExternalBytes int64            `json:"external_bytes"`
	ExternalCount int64            `json:"external_count"`
	MissingCount  int64            `json:"missing_count"`
	ExternalTop   []ExternalObject `json:"external_top,omitempty"`
}

// ExternalObject 不在本服务记录中的对象
type ExternalObject struct {
	Key  string `json:"key"`
	Size int64  `json:"size"`
}

// DiffWithKeys 用给定的本地 key 列表与桶内对象做差集
func (u BucketUsage) DiffWithKeys(localKeys []string, topN int) UsageStats {
	stats := UsageStats{
		BucketBytes:   u.Bytes,
		BucketObjects: u.Objects,
	}
	if !u.FetchedAt.IsZero() {
		stats.FetchedAt = u.FetchedAt.Format("2006-01-02 15:04:05")
	}
	if u.Err != nil {
		stats.Error = u.Err.Error()
	}
	if len(u.Keys) == 0 {
		return stats
	}

	live := make(map[string]struct{}, len(localKeys))
	for _, k := range localKeys {
		live[k] = struct{}{}
	}

	type kv struct {
		key  string
		size int64
	}
	var external []kv
	for k, size := range u.Keys {
		if _, ok := live[k]; ok {
			continue
		}
		stats.ExternalBytes += size
		stats.ExternalCount++
		if topN > 0 {
			external = append(external, kv{key: k, size: size})
		}
	}
	for k := range live {
		if _, ok := u.Keys[k]; !ok {
			stats.MissingCount++
		}
	}

	if topN > 0 && len(external) > 0 {
		sort.Slice(external, func(i, j int) bool { return external[i].size > external[j].size })
		if len(external) > topN {
			external = external[:topN]
		}
		for _, e := range external {
			stats.ExternalTop = append(stats.ExternalTop, ExternalObject{Key: e.key, Size: e.size})
		}
	}
	return stats
}

// InvalidateUsageCache 主动让缓存失效（例如批量删除后希望立刻刷新）
func (r *R2Client) InvalidateUsageCache() {
	bucketUsage.mu.Lock()
	bucketUsage.usage.FetchedAt = time.Time{}
	bucketUsage.mu.Unlock()
}
