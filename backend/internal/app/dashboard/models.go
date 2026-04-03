package dashboard

import (
	"context"
	"time"
)

// Repository 定义仪表盘域持久化接口。
type Repository interface {
	LoadStatsSnapshot(ctx context.Context, todayStart, fiveMinAgo time.Time, userID int) (StatsSnapshot, error)
	ListTrendLogs(ctx context.Context, startTime, endTime time.Time, userID int) ([]TrendLog, error)
}

// StatsSnapshot 表示从存储层读取的原始统计快照。
type StatsSnapshot struct {
	TotalAPIKeys        int64
	EnabledAPIKeys      int64
	TotalAccounts       int64
	EnabledAccounts     int64
	ErrorAccounts       int64
	TotalUsers          int64
	NewUsersToday       int64
	TodayRequests       int64
	AllTimeRequests     int64
	TodayTokens         int64
	TodayCost           float64
	TodayStandardCost   float64
	TodayDurationMs     int64
	ActiveUsers         int64
	AllTimeTokens       int64
	AllTimeCost         float64
	AllTimeStandardCost float64
	RecentRequests      int64
	RecentTokens        int64
}

// Stats 表示仪表盘统计结果。
type Stats struct {
	TotalAPIKeys        int64
	EnabledAPIKeys      int64
	TotalAccounts       int64
	EnabledAccounts     int64
	ErrorAccounts       int64
	TodayRequests       int64
	AllTimeRequests     int64
	TotalUsers          int64
	NewUsersToday       int64
	TodayTokens         int64
	TodayCost           float64
	TodayStandardCost   float64
	AllTimeTokens       int64
	AllTimeCost         float64
	AllTimeStandardCost float64
	RPM                 float64
	TPM                 float64
	AvgDurationMs       float64
	ActiveUsers         int64
}

// TrendQuery 表示趋势查询参数。
type TrendQuery struct {
	Range       string
	Granularity string
	StartDate   string
	EndDate     string
	UserID      int
}

// Trend 表示仪表盘趋势结果。
type Trend struct {
	ModelDistribution []ModelStats
	UserRanking       []UserRanking
	TokenTrend        []TimeBucket
	TopUsers          []UserTrend
}

// TrendLog 表示趋势聚合所需的使用日志。
type TrendLog struct {
	UserID            int
	UserEmail         string
	Model             string
	InputTokens       int64
	OutputTokens      int64
	CachedInputTokens int64
	ActualCost        float64
	StandardCost      float64
	CreatedAt         time.Time
}

// ModelStats 表示模型分布统计。
type ModelStats struct {
	Model        string
	Requests     int64
	Tokens       int64
	ActualCost   float64
	StandardCost float64
}

// UserRanking 表示用户消费排行。
type UserRanking struct {
	UserID       int64
	Email        string
	Requests     int64
	Tokens       int64
	ActualCost   float64
	StandardCost float64
}

// TimeBucket 表示趋势时间桶。
type TimeBucket struct {
	Time         string
	InputTokens  int64
	OutputTokens int64
	CachedInput  int64
}

// UserTrend 表示单个用户的趋势。
type UserTrend struct {
	UserID int64
	Email  string
	Trend  []UserTrendPoint
}

// UserTrendPoint 表示单个用户趋势点。
type UserTrendPoint struct {
	Time   string
	Tokens int64
}
