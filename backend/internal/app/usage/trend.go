package usage

import (
	"sort"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/pkg/timezone"
)

// BuildTrendBuckets 生成趋势时间桶。
// tz 决定时间桶的对齐时区；为空时回退到服务器本地时区。
// 之前的实现固定按 UTC 格式化，导致非 UTC 用户看到的小时/天与本地不一致。
func BuildTrendBuckets(entries []TrendEntry, granularity, tz string) []TrendBucket {
	timeFmt := "2006-01-02"
	if granularity == "hour" {
		timeFmt = "2006-01-02 15:00"
	}
	loc := timezone.Resolve(tz)

	bucketMap := make(map[string]*TrendBucket)
	for _, entry := range entries {
		createdAt, err := time.Parse(time.RFC3339, entry.CreatedAt)
		if err != nil {
			continue
		}
		key := createdAt.In(loc).Format(timeFmt)
		bucket, ok := bucketMap[key]
		if !ok {
			bucket = &TrendBucket{Time: key}
			bucketMap[key] = bucket
		}
		bucket.InputTokens += entry.InputTokens
		bucket.OutputTokens += entry.OutputTokens
		bucket.CacheRead += entry.CachedInputTokens
		bucket.CacheCreation += entry.CacheCreationTokens
		bucket.ActualCost += entry.ActualCost
		bucket.StandardCost += entry.StandardCost
		bucket.BilledCost += entry.BilledCost
	}

	result := make([]TrendBucket, 0, len(bucketMap))
	for _, bucket := range bucketMap {
		result = append(result, *bucket)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Time < result[j].Time
	})
	return result
}
