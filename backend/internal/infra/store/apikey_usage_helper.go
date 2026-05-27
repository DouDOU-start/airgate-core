package store

import (
	"context"
	"time"

	"entgo.io/ent/dialect/sql"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/predicate"
	entusagelog "github.com/DouDOU-start/airgate-core/ent/usagelog"
)

// queryAPIKeyUsage 返回每个 key 的"今日"和"近 30 天"实际成本。
// todayStart 必须由调用方按用户时区计算好；近 30 天窗口以 todayStart 为锚。
func queryAPIKeyUsage(ctx context.Context, db *ent.Client, keyIDs []int, todayStart time.Time) (map[int]float64, map[int]float64, error) {
	todayMap := make(map[int]float64, len(keyIDs))
	thirtyDayMap := make(map[int]float64, len(keyIDs))
	if len(keyIDs) == 0 {
		return todayMap, thirtyDayMap, nil
	}

	thirtyDaysAgo := todayStart.AddDate(0, 0, -29)

	type costRow struct {
		APIKeyID int     `json:"api_key_usage_logs"`
		Cost     float64 `json:"cost"`
	}

	var todayRows []costRow
	if err := db.UsageLog.Query().
		Where(
			usageLogColumnIn(entusagelog.APIKeyColumn, keyIDs),
			entusagelog.CreatedAtGTE(todayStart),
		).
		GroupBy(entusagelog.ForeignKeys[0]).
		Aggregate(ent.As(ent.Sum(entusagelog.FieldActualCost), "cost")).
		Scan(ctx, &todayRows); err != nil {
		return nil, nil, err
	}
	for _, row := range todayRows {
		todayMap[row.APIKeyID] = row.Cost
	}

	var thirtyDayRows []costRow
	if err := db.UsageLog.Query().
		Where(
			usageLogColumnIn(entusagelog.APIKeyColumn, keyIDs),
			entusagelog.CreatedAtGTE(thirtyDaysAgo),
		).
		GroupBy(entusagelog.ForeignKeys[0]).
		Aggregate(ent.As(ent.Sum(entusagelog.FieldActualCost), "cost")).
		Scan(ctx, &thirtyDayRows); err != nil {
		return nil, nil, err
	}
	for _, row := range thirtyDayRows {
		thirtyDayMap[row.APIKeyID] = row.Cost
	}

	return todayMap, thirtyDayMap, nil
}

func usageLogColumnIn(column string, values []int) predicate.UsageLog {
	return predicate.UsageLog(func(s *sql.Selector) {
		s.Where(sql.InInts(s.C(column), values...))
	})
}
