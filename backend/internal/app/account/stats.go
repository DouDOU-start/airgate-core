package account

import (
	"sort"
	"time"
)

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
func ResolveStatsRange(now time.Time, query StatsQuery) (time.Time, time.Time, error) {
	location := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)

	var startDate time.Time
	if query.StartDate != "" {
		parsed, err := time.ParseInLocation("2006-01-02", query.StartDate, location)
		if err != nil {
			return time.Time{}, time.Time{}, ErrInvalidDateRange
		}
		startDate = parsed
	}

	var endDate time.Time
	if query.EndDate != "" {
		parsed, err := time.ParseInLocation("2006-01-02", query.EndDate, location)
		if err != nil {
			return time.Time{}, time.Time{}, ErrInvalidDateRange
		}
		endDate = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 23, 59, 59, 0, location)
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
func BuildStatsResult(account Account, logs []UsageLog, now, startDate, endDate time.Time) StatsResult {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	totalDays := int(endDate.Sub(startDate).Hours()/24) + 1

	result := StatsResult{
		AccountID: account.ID,
		Name:      account.Name,
		Platform:  account.Platform,
		Status:    account.Status,
		StartDate: startDate.Format("2006-01-02"),
		EndDate:   endDate.Format("2006-01-02"),
		TotalDays: totalDays,
	}

	dailyMap := make(map[string]*DailyStats)
	modelMap := make(map[string]*ModelStats)
	var totalDurationMs int64

	for _, log := range logs {
		dateKey := log.CreatedAt.Format("2006-01-02")

		result.Range.Count++
		result.Range.InputTokens += log.InputTokens
		result.Range.OutputTokens += log.OutputTokens
		result.Range.TotalCost += log.TotalCost
		result.Range.ActualCost += log.ActualCost
		totalDurationMs += log.DurationMs

		if !log.CreatedAt.Before(today) {
			result.Today.Count++
			result.Today.InputTokens += log.InputTokens
			result.Today.OutputTokens += log.OutputTokens
			result.Today.TotalCost += log.TotalCost
			result.Today.ActualCost += log.ActualCost
		}

		if stats, ok := dailyMap[dateKey]; ok {
			stats.Count++
			stats.TotalCost += log.TotalCost
			stats.ActualCost += log.ActualCost
		} else {
			dailyMap[dateKey] = &DailyStats{
				Date:       dateKey,
				Count:      1,
				TotalCost:  log.TotalCost,
				ActualCost: log.ActualCost,
			}
		}

		if stats, ok := modelMap[log.Model]; ok {
			stats.Count++
			stats.InputTokens += log.InputTokens
			stats.OutputTokens += log.OutputTokens
			stats.TotalCost += log.TotalCost
			stats.ActualCost += log.ActualCost
		} else {
			modelMap[log.Model] = &ModelStats{
				Model:        log.Model,
				Count:        1,
				InputTokens:  log.InputTokens,
				OutputTokens: log.OutputTokens,
				TotalCost:    log.TotalCost,
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
		if daily.TotalCost > result.PeakCostDay.TotalCost {
			result.PeakCostDay = PeakDay{
				Date:       daily.Date,
				Count:      daily.Count,
				TotalCost:  daily.TotalCost,
				ActualCost: daily.ActualCost,
			}
		}
		if daily.Count > result.PeakRequestDay.Count {
			result.PeakRequestDay = PeakDay{
				Date:       daily.Date,
				Count:      daily.Count,
				TotalCost:  daily.TotalCost,
				ActualCost: daily.ActualCost,
			}
		}
	}

	return result
}
