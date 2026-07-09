// Package upstreamlog 提供失败请求留痕（用户转发失败 + 渠道测试失败）的查询用例。
// 写入侧见 internal/errlog（异步 sink），本包只负责读取。
package upstreamlog

import (
	"context"
	"encoding/json"

	"github.com/DouDOU-start/airgate-core/internal/pkg/pagination"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// ListFilter 失败请求留痕列表筛选。
type ListFilter struct {
	Page      int
	PageSize  int
	Source    string // 发起方：relay / channel_test
	Phase     string
	UserID    *int64
	APIKeyID  *int64
	ChannelID *int64
	RequestID string
	Model     string
	StartDate string
	EndDate   string
	TZ        string
}

// Record 上游请求日志领域对象。
type Record struct {
	ID           int64
	RequestID    string
	Source       string
	Phase        string
	StatusCode   int
	ErrorType    string
	ErrorCode    string
	Message      string
	Attempts     int
	AttemptChain json.RawMessage
	Billed       bool
	Model        string
	Endpoint     string
	Stream       bool
	UserID       int
	UserEmail    string
	APIKeyID     int
	GroupID      int
	ChannelID    int
	ChannelName  string
	IPAddress    string
	UserAgent    string
	DurationMs   int64
	RepeatCount  int
	CreatedAt    string
}

// ListResult 分页结果。
type ListResult struct {
	List     []Record
	Total    int64
	Page     int
	PageSize int
}

// Repository 上游请求日志仓储接口。
type Repository interface {
	List(context.Context, ListFilter) ([]Record, error)
	Count(context.Context, ListFilter) (int64, error)
}

// FailureCounter 渠道失败分钟桶读取（由 errlog.Recorder 实现，Redis 为错误率事实源）。
type FailureCounter interface {
	FailureCounts(ctx context.Context, channelIDs []int, minutes int) (map[int]map[string]int64, error)
}

// Service 上游请求日志查询服务。
type Service struct {
	repo    Repository
	counter FailureCounter
}

// SetFailureCounter 注入失败计数读取器（服务器装配期调用；未注入时统计接口返回空）。
func (s *Service) SetFailureCounter(counter FailureCounter) {
	s.counter = counter
}

// ChannelFailureStats 渠道近 minutes 分钟失败计数（渠道页监控列数据源）。
func (s *Service) ChannelFailureStats(ctx context.Context, channelIDs []int, minutes int) (map[int]map[string]int64, error) {
	if s.counter == nil || len(channelIDs) == 0 {
		return map[int]map[string]int64{}, nil
	}
	counts, err := s.counter.FailureCounts(ctx, channelIDs, minutes)
	if err != nil {
		sdk.LoggerFromContext(ctx).Error("upstream_log_query_failed", "scope", "failure_counts", sdk.LogFieldError, err)
		return nil, err
	}
	if counts == nil {
		counts = map[int]map[string]int64{}
	}
	return counts, nil
}

// NewService 创建查询服务。
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List 管理员分页查询。
func (s *Service) List(ctx context.Context, filter ListFilter) (ListResult, error) {
	page, pageSize := pagination.Normalize(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, err := s.repo.List(ctx, filter)
	if err != nil {
		sdk.LoggerFromContext(ctx).Error("upstream_log_query_failed", "scope", "list", sdk.LogFieldError, err)
		return ListResult{}, err
	}
	total, err := s.repo.Count(ctx, filter)
	if err != nil {
		sdk.LoggerFromContext(ctx).Error("upstream_log_query_failed", "scope", "count", sdk.LogFieldError, err)
		return ListResult{}, err
	}
	return ListResult{List: list, Total: total, Page: page, PageSize: pageSize}, nil
}
