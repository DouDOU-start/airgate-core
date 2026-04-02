package store

import (
	"context"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	entapikey "github.com/DouDOU-start/airgate-core/ent/apikey"
	entusagelog "github.com/DouDOU-start/airgate-core/ent/usagelog"
)

func queryAPIKeyUsage(ctx context.Context, db *ent.Client, keyIDs []int) (map[int]float64, map[int]float64, error) {
	todayMap := make(map[int]float64, len(keyIDs))
	thirtyDayMap := make(map[int]float64, len(keyIDs))
	if len(keyIDs) == 0 {
		return todayMap, thirtyDayMap, nil
	}

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	thirtyDaysAgo := now.AddDate(0, 0, -30)

	type costRow struct {
		APIKeyID int     `json:"api_key_usage_logs"`
		Cost     float64 `json:"cost"`
	}

	var todayRows []costRow
	if err := db.UsageLog.Query().
		Where(
			entusagelog.HasAPIKeyWith(entapikey.IDIn(keyIDs...)),
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
			entusagelog.HasAPIKeyWith(entapikey.IDIn(keyIDs...)),
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
