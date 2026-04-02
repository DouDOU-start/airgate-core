package usage

import (
	"sort"
	"time"
)

const (
	defaultPage     = 1
	defaultPageSize = 20
)

// NormalizePage 将分页参数规范化。
func NormalizePage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = defaultPage
	}
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	return page, pageSize
}

// BuildTrendBuckets 生成趋势时间桶。
func BuildTrendBuckets(entries []TrendEntry, granularity string) []TrendBucket {
	timeFmt := "2006-01-02"
	if granularity == "hour" {
		timeFmt = "2006-01-02 15:00"
	}

	bucketMap := make(map[string]*TrendBucket)
	for _, entry := range entries {
		createdAt, err := time.Parse(time.RFC3339, entry.CreatedAt)
		if err != nil {
			continue
		}
		key := createdAt.Format(timeFmt)
		bucket, ok := bucketMap[key]
		if !ok {
			bucket = &TrendBucket{Time: key}
			bucketMap[key] = bucket
		}
		bucket.InputTokens += entry.InputTokens
		bucket.OutputTokens += entry.OutputTokens
		bucket.CacheRead += entry.CachedInputTokens
		bucket.ActualCost += entry.ActualCost
		bucket.StandardCost += entry.StandardCost
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
