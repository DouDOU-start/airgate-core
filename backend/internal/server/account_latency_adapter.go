package server

import (
	"context"
	"strings"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	entattempt "github.com/DouDOU-start/airgate-core/ent/requestauditattempt"
	entlog "github.com/DouDOU-start/airgate-core/ent/requestauditlog"
	entusagelog "github.com/DouDOU-start/airgate-core/ent/usagelog"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/relay/pipeline"
)

const accountFirstTokenAuditBatchSize = 500

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
			entusagelog.FieldRequestID,
		).
		Order(ent.Desc(entusagelog.FieldCreatedAt)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}
	multiAttemptRequestIDs, err := s.loadMultiAttemptRequestIDs(ctx, rows)
	if err != nil {
		return nil, err
	}
	samples := make([]pipeline.AccountFirstTokenSample, 0, len(rows))
	for _, row := range rows {
		if _, excluded := multiAttemptRequestIDs[strings.TrimSpace(row.RequestID)]; excluded {
			continue
		}
		samples = append(samples, pipeline.AccountFirstTokenSample{
			AccountID: row.AccountID, Model: row.Model,
			FirstTokenMs: row.FirstTokenMs, CreatedAt: row.CreatedAt,
		})
	}
	return samples, nil
}

// LoadRecentChannelFirstTokenSamples 从使用记录中读取近期渠道成功首字。
// 使用记录保存请求级首字，因此与账号预热相同，排除发生过故障转移的请求。
func (s accountFirstTokenStore) LoadRecentChannelFirstTokenSamples(
	ctx context.Context,
	since time.Time,
	limit int,
) ([]pipeline.ChannelFirstTokenSample, error) {
	if s.db == nil || limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.UsageLog.Query().
		Where(
			entusagelog.ChannelKeyIDNotNil(),
			entusagelog.CreatedAtGTE(since),
			entusagelog.StreamEQ(true),
			entusagelog.UsageStatusEQ(billing.UsageStatusCompleted),
			entusagelog.FirstTokenMsGT(0),
		).
		Select(
			entusagelog.FieldChannelKeyID,
			entusagelog.FieldModel,
			entusagelog.FieldFirstTokenMs,
			entusagelog.FieldCreatedAt,
			entusagelog.FieldRequestID,
		).
		Order(ent.Desc(entusagelog.FieldCreatedAt)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}
	multiAttemptRequestIDs, err := s.loadMultiAttemptRequestIDs(ctx, rows)
	if err != nil {
		return nil, err
	}
	samples := make([]pipeline.ChannelFirstTokenSample, 0, len(rows))
	for _, row := range rows {
		if _, excluded := multiAttemptRequestIDs[strings.TrimSpace(row.RequestID)]; excluded {
			continue
		}
		samples = append(samples, pipeline.ChannelFirstTokenSample{
			ChannelKeyID: row.ChannelKeyID, Model: row.Model,
			FirstTokenMs: row.FirstTokenMs, CreatedAt: row.CreatedAt,
		})
	}
	return samples, nil
}

// loadMultiAttemptRequestIDs 找出发生过调度目标切换或重试的请求。usage_logs 的首字是
// 请求级总耗时，不能在启动预热时把前序失败耗时归因给最终成功目标。
// 找不到审计记录时保留样本，兼容审计已清理或旧版本尚未写入审计的历史数据。
func (s accountFirstTokenStore) loadMultiAttemptRequestIDs(
	ctx context.Context,
	usageRows []*ent.UsageLog,
) (map[string]struct{}, error) {
	requestIDs := make([]string, 0, len(usageRows))
	seenRequestIDs := make(map[string]struct{}, len(usageRows))
	for _, row := range usageRows {
		requestID := strings.TrimSpace(row.RequestID)
		if requestID == "" {
			continue
		}
		if _, exists := seenRequestIDs[requestID]; exists {
			continue
		}
		seenRequestIDs[requestID] = struct{}{}
		requestIDs = append(requestIDs, requestID)
	}
	if len(requestIDs) == 0 {
		return nil, nil
	}

	auditRequestIDs := make(map[int]string, len(requestIDs))
	for start := 0; start < len(requestIDs); start += accountFirstTokenAuditBatchSize {
		end := min(start+accountFirstTokenAuditBatchSize, len(requestIDs))
		rows, err := s.db.RequestAuditLog.Query().
			Where(entlog.RequestIDIn(requestIDs[start:end]...)).
			Select(entlog.FieldID, entlog.FieldRequestID).
			All(ctx)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			auditRequestIDs[row.ID] = row.RequestID
		}
	}
	if len(auditRequestIDs) == 0 {
		return nil, nil
	}

	auditIDs := make([]int, 0, len(auditRequestIDs))
	for auditID := range auditRequestIDs {
		auditIDs = append(auditIDs, auditID)
	}
	multiAttemptRequestIDs := make(map[string]struct{})
	for start := 0; start < len(auditIDs); start += accountFirstTokenAuditBatchSize {
		end := min(start+accountFirstTokenAuditBatchSize, len(auditIDs))
		var counts []struct {
			RequestAuditID int `json:"request_audit_id"`
			Count          int `json:"count"`
		}
		if err := s.db.RequestAuditAttempt.Query().
			Where(entattempt.RequestAuditIDIn(auditIDs[start:end]...)).
			GroupBy(entattempt.FieldRequestAuditID).
			Aggregate(ent.Count()).
			Scan(ctx, &counts); err != nil {
			return nil, err
		}
		for _, count := range counts {
			if count.Count > 1 {
				multiAttemptRequestIDs[auditRequestIDs[count.RequestAuditID]] = struct{}{}
			}
		}
	}
	return multiAttemptRequestIDs, nil
}
