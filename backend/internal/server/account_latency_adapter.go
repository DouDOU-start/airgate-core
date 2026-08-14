package server

import (
	"context"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	entusagelog "github.com/DouDOU-start/airgate-core/ent/usagelog"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/relay/pipeline"
)

// accountFirstTokenStore 从使用记录中读取近期账号成功首字，供调度器启动预热。
type accountFirstTokenStore struct {
	db *ent.Client
}

func (s accountFirstTokenStore) LoadRecentAccountFirstTokenSamples(
	ctx context.Context,
	since time.Time,
	limit int,
) ([]pipeline.AccountFirstTokenSample, error) {
	if s.db == nil || limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.UsageLog.Query().
		Where(
			entusagelog.AccountIDNotNil(),
			entusagelog.CreatedAtGTE(since),
			entusagelog.StreamEQ(true),
			entusagelog.UsageStatusEQ(billing.UsageStatusCompleted),
			entusagelog.FirstTokenMsGT(0),
		).
		Select(
			entusagelog.FieldAccountID,
			entusagelog.FieldModel,
			entusagelog.FieldFirstTokenMs,
			entusagelog.FieldCreatedAt,
		).
		Order(ent.Desc(entusagelog.FieldCreatedAt)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}
	samples := make([]pipeline.AccountFirstTokenSample, 0, len(rows))
	for _, row := range rows {
		samples = append(samples, pipeline.AccountFirstTokenSample{
			AccountID: row.AccountID, Model: row.Model,
			FirstTokenMs: row.FirstTokenMs, CreatedAt: row.CreatedAt,
		})
	}
	return samples, nil
}
