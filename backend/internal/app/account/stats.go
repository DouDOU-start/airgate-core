package account

import (
	"sort"
	"strings"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/pkg/timezone"
)

// isImageModel 判断 usage_log.model 是否属于生图家族。
// 与 scheduler.ModelFamily 的 "gpt-image-*" 规则保持一致 —— 不直接 import scheduler
// 包是为了避免 stats 这一层依赖调度模块。两边都改时记得同步更新。
func isImageModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "gpt-image")
}

const (
	defaultPage     = 1
	defaultPageSize = 20
)

// NormalizePage 将分页参数规整为安全值。
func NormalizePage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = defaultPage
	}
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	return page, pageSize
}

// ResolveStatsRange 解析统计时间范围。
// now 应已处于调用方时区（即 now.In(timezone.Resolve(query.TZ))），
// 这样 today / startDate / endDate 都按用户期望的"本地一天"对齐。
func ResolveStatsRange(now time.Time, query StatsQuery) (time.Time, time.Time, error) {
	location := now.Location()
	today := timezone.StartOfDay(now)

	var startDate time.Time
	if query.StartDate != "" {
		parsed, err := timezone.ParseDate(query.StartDate, location)
		if err != nil {
			return time.Time{}, time.Time{}, ErrInvalidDateRange
		}
		startDate = parsed
	}

	var endDate time.Time
	if query.EndDate != "" {
		parsed, err := timezone.ParseDate(query.EndDate, location)
		if err != nil {
			return time.Time{}, time.Time{}, ErrInvalidDateRange
		}
		endDate = timezone.EndOfDay(parsed)
	}

	if startDate.IsZero() {
		startDate = today.AddDate(0, 0, -29)
	}
	if endDate.IsZero() {
		endDate = now
	}
	if endDate.Before(startDate) {
		return time.Time{}, time.Time{}, ErrInvalidDateRange
	}

	return startDate, endDate, nil
}

// BuildStatsResult 聚合账号统计结果。
// 入参 now 同样应已处于调用方时区。
func BuildStatsResult(account Account, logs []UsageLog, now, startDate, endDate time.Time) StatsResult {
	location := now.Location()
	today := timezone.StartOfDay(now)
	totalDays := int(endDate.Sub(startDate).Hours()/24) + 1

	result := StatsResult{
		AccountID: account.ID,
		Name:      account.Name,
		Platform:  account.Platform,
		State:     account.State,
		StartDate: startDate.Format("2006-01-02"),
		EndDate:   endDate.Format("2006-01-02"),
		TotalDays: totalDays,
	}

	dailyMap := make(map[string]*DailyStats)
	modelMap := make(map[string]*ModelStats)
	var totalDurationMs int64

	for _, log := range logs {
		// 按用户时区对齐日期 key，避免 UTC 切换导致跨日错位
		dateKey := log.CreatedAt.In(location).Format("2006-01-02")
		isImage := isImageModel(log.Model)

		result.Range.Count++
		result.Range.InputTokens += log.InputTokens
		result.Range.OutputTokens += log.OutputTokens
		result.Range.TotalCost += log.TotalCost
		result.Range.AccountCost += log.AccountCost
		result.Range.ActualCost += log.ActualCost
		totalDurationMs += log.DurationMs
		if isImage {
			result.Range.ImageCount++
			result.Range.ImageCost += log.AccountCost
		}

		if !log.CreatedAt.Before(today) {
			result.Today.Count++
			result.Today.InputTokens += log.InputTokens
			result.Today.OutputTokens += log.OutputTokens
			result.Today.TotalCost += log.TotalCost
			result.Today.AccountCost += log.AccountCost
			result.Today.ActualCost += log.ActualCost
			if isImage {
				result.Today.ImageCount++
				result.Today.ImageCost += log.AccountCost
			}
		}

		if stats, ok := dailyMap[dateKey]; ok {
			stats.Count++
			stats.TotalCost += log.TotalCost
			stats.AccountCost += log.AccountCost
			stats.ActualCost += log.ActualCost
		} else {
			dailyMap[dateKey] = &DailyStats{
				Date:        dateKey,
				Count:       1,
				TotalCost:   log.TotalCost,
				AccountCost: log.AccountCost,
				ActualCost:  log.ActualCost,
			}
		}

		if stats, ok := modelMap[log.Model]; ok {
			stats.Count++
			stats.InputTokens += log.InputTokens
			stats.OutputTokens += log.OutputTokens
			stats.TotalCost += log.TotalCost
			stats.AccountCost += log.AccountCost
			stats.ActualCost += log.ActualCost
		} else {
			modelMap[log.Model] = &ModelStats{
				Model:        log.Model,
				Count:        1,
				InputTokens:  log.InputTokens,
				OutputTokens: log.OutputTokens,
				TotalCost:    log.TotalCost,
				AccountCost:  log.AccountCost,
				ActualCost:   log.ActualCost,
			}
		}
	}

	result.DailyTrend = make([]DailyStats, 0, totalDays)
	for date := startDate; !date.After(endDate); date = date.AddDate(0, 0, 1) {
		key := date.Format("2006-01-02")
		if daily, ok := dailyMap[key]; ok {
			result.DailyTrend = append(result.DailyTrend, *daily)
		} else {
			result.DailyTrend = append(result.DailyTrend, DailyStats{Date: key})
		}
	}

	result.Models = make([]ModelStats, 0, len(modelMap))
	for _, model := range modelMap {
		result.Models = append(result.Models, *model)
	}
	sort.Slice(result.Models, func(i, j int) bool {
		if result.Models[i].Count == result.Models[j].Count {
			return result.Models[i].Model < result.Models[j].Model
		}
		return result.Models[i].Count > result.Models[j].Count
	})

	result.ActiveDays = len(dailyMap)
	if result.Range.Count > 0 {
		result.AvgDurationMs = totalDurationMs / int64(result.Range.Count)
	}

	for _, daily := range dailyMap {
		// "最高费用日" 用 AccountCost（账号实际成本）做比较，
		// 这才是用户期望的"哪天这个账号花得最多"。
		if daily.AccountCost > result.PeakCostDay.AccountCost {
			result.PeakCostDay = PeakDay{
				Date:        daily.Date,
				Count:       daily.Count,
				TotalCost:   daily.TotalCost,
				AccountCost: daily.AccountCost,
				ActualCost:  daily.ActualCost,
			}
		}
		if daily.Count > result.PeakRequestDay.Count {
			result.PeakRequestDay = PeakDay{
				Date:        daily.Date,
				Count:       daily.Count,
				TotalCost:   daily.TotalCost,
				AccountCost: daily.AccountCost,
				ActualCost:  daily.ActualCost,
			}
		}
	}

	return result
}
