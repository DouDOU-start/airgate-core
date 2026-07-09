package redemption

import (
	"context"
	"errors"
	"testing"
	"time"
)

// stubRepository 表驱动测试用桩实现。
type stubRepository struct {
	createBatch  func(context.Context, []Code) ([]Code, error)
	list         func(context.Context, ListFilter, time.Time) ([]Code, int64, error)
	stats        func(context.Context, time.Time) (Stats, error)
	findByID     func(context.Context, int) (Code, error)
	updateStatus func(context.Context, int, string, string) error
	deleteFn     func(context.Context, int) error
	redeem       func(context.Context, string, int, time.Time) (RedeemResult, error)
}

func (s stubRepository) CreateBatch(ctx context.Context, codes []Code) ([]Code, error) {
	return s.createBatch(ctx, codes)
}
func (s stubRepository) List(ctx context.Context, f ListFilter, now time.Time) ([]Code, int64, error) {
	return s.list(ctx, f, now)
}
func (s stubRepository) Stats(ctx context.Context, now time.Time) (Stats, error) {
	return s.stats(ctx, now)
}
func (s stubRepository) FindByID(ctx context.Context, id int) (Code, error) {
	return s.findByID(ctx, id)
}
func (s stubRepository) UpdateStatus(ctx context.Context, id int, from, to string) error {
	return s.updateStatus(ctx, id, from, to)
}
func (s stubRepository) Delete(ctx context.Context, id int) error {
	return s.deleteFn(ctx, id)
}
func (s stubRepository) Redeem(ctx context.Context, code string, userID int, now time.Time) (RedeemResult, error) {
	return s.redeem(ctx, code, userID, now)
}

func TestGenerateValidatesInput(t *testing.T) {
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)
	tests := []struct {
		name    string
		input   GenerateInput
		wantErr error
	}{
		{"count 为 0", GenerateInput{Count: 0, Value: 10}, ErrInvalidGenerateInput},
		{"count 超上限", GenerateInput{Count: maxBatchCount + 1, Value: 10}, ErrInvalidGenerateInput},
		{"面值为 0", GenerateInput{Count: 1, Value: 0}, ErrInvalidGenerateInput},
		{"面值为负", GenerateInput{Count: 1, Value: -5}, ErrInvalidGenerateInput},
		{"备注超长", GenerateInput{Count: 1, Value: 10, Remark: string(make([]byte, maxRemarkLen+1))}, ErrInvalidGenerateInput},
		{"过期时间在过去", GenerateInput{Count: 1, Value: 10, ExpiresAt: &past}, ErrInvalidGenerateInput},
		{"合法输入", GenerateInput{Count: 3, Value: 10, Remark: "活动", ExpiresAt: &future}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewService(stubRepository{
				createBatch: func(_ context.Context, codes []Code) ([]Code, error) {
					return codes, nil
				},
			})
			created, err := service.Generate(t.Context(), tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Generate() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && len(created) != tt.input.Count {
				t.Fatalf("Generate() created %d codes, want %d", len(created), tt.input.Count)
			}
		})
	}
}

func TestGenerateProducesUniqueRandomCodes(t *testing.T) {
	service := NewService(stubRepository{
		createBatch: func(_ context.Context, codes []Code) ([]Code, error) {
			return codes, nil
		},
	})
	created, err := service.Generate(t.Context(), GenerateInput{Count: 50, Value: 1})
	if err != nil {
		t.Fatalf("Generate() returned error: %v", err)
	}
	seen := make(map[string]struct{}, len(created))
	for _, c := range created {
		if len(c.Code) != 32 {
			t.Fatalf("code %q length = %d, want 32", c.Code, len(c.Code))
		}
		if _, dup := seen[c.Code]; dup {
			t.Fatalf("duplicate code generated: %s", c.Code)
		}
		seen[c.Code] = struct{}{}
		if c.Status != StatusUnused {
			t.Fatalf("code status = %s, want unused", c.Status)
		}
	}
}

func TestSetDisabledTransitions(t *testing.T) {
	tests := []struct {
		name     string
		disabled bool
		wantFrom string
		wantTo   string
	}{
		{"停用", true, StatusUnused, StatusDisabled},
		{"恢复", false, StatusDisabled, StatusUnused},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotFrom, gotTo string
			service := NewService(stubRepository{
				updateStatus: func(_ context.Context, _ int, from, to string) error {
					gotFrom, gotTo = from, to
					return nil
				},
			})
			if err := service.SetDisabled(t.Context(), 1, tt.disabled); err != nil {
				t.Fatalf("SetDisabled() returned error: %v", err)
			}
			if gotFrom != tt.wantFrom || gotTo != tt.wantTo {
				t.Fatalf("SetDisabled() transition %s→%s, want %s→%s", gotFrom, gotTo, tt.wantFrom, tt.wantTo)
			}
		})
	}
}

func TestDeleteRejectsUsedCode(t *testing.T) {
	service := NewService(stubRepository{
		findByID: func(_ context.Context, _ int) (Code, error) {
			return Code{ID: 1, Status: StatusUsed}, nil
		},
	})
	if err := service.Delete(t.Context(), 1); !errors.Is(err, ErrCodeStateConflict) {
		t.Fatalf("Delete() error = %v, want ErrCodeStateConflict", err)
	}
}

func TestRedeemNormalizesAndValidatesCode(t *testing.T) {
	var gotCode string
	service := NewService(stubRepository{
		redeem: func(_ context.Context, code string, _ int, _ time.Time) (RedeemResult, error) {
			gotCode = code
			return RedeemResult{Value: 10, Balance: 20}, nil
		},
	})

	if _, err := service.Redeem(t.Context(), 1, "   "); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("Redeem(空白码) error = %v, want ErrCodeNotFound", err)
	}

	result, err := service.Redeem(t.Context(), 1, "  abc123  ")
	if err != nil {
		t.Fatalf("Redeem() returned error: %v", err)
	}
	if gotCode != "abc123" {
		t.Fatalf("Redeem() passed code %q, want trimmed \"abc123\"", gotCode)
	}
	if result.Value != 10 || result.Balance != 20 {
		t.Fatalf("Redeem() result = %+v", result)
	}
}

func TestRedeemRateLimitsGuessFailures(t *testing.T) {
	now := time.Now()
	service := NewService(stubRepository{
		redeem: func(_ context.Context, _ string, _ int, _ time.Time) (RedeemResult, error) {
			return RedeemResult{}, ErrCodeNotFound
		},
	})
	service.nowFunc = func() time.Time { return now }

	// 连续猜码失败 failLimit 次后触发限流
	for i := 0; i < failLimit; i++ {
		if _, err := service.Redeem(t.Context(), 1, "guess"); !errors.Is(err, ErrCodeNotFound) {
			t.Fatalf("第 %d 次 Redeem() error = %v, want ErrCodeNotFound", i+1, err)
		}
	}
	if _, err := service.Redeem(t.Context(), 1, "guess"); !errors.Is(err, ErrRedeemRateLimited) {
		t.Fatalf("超限后 Redeem() error = %v, want ErrRedeemRateLimited", err)
	}

	// 其他用户不受影响
	if _, err := service.Redeem(t.Context(), 2, "guess"); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("其他用户 Redeem() error = %v, want ErrCodeNotFound", err)
	}

	// 窗口滑过后恢复
	now = now.Add(failWindow + time.Second)
	if _, err := service.Redeem(t.Context(), 1, "guess"); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("窗口过后 Redeem() error = %v, want ErrCodeNotFound", err)
	}
}

func TestRedeemSystemErrorNotCountedInRateLimit(t *testing.T) {
	sysErr := errors.New("db down")
	failing := true
	service := NewService(stubRepository{
		redeem: func(_ context.Context, _ string, _ int, _ time.Time) (RedeemResult, error) {
			if failing {
				return RedeemResult{}, sysErr
			}
			return RedeemResult{Value: 1, Balance: 1}, nil
		},
	})

	for i := 0; i < failLimit*2; i++ {
		if _, err := service.Redeem(t.Context(), 1, "code"); !errors.Is(err, sysErr) {
			t.Fatalf("Redeem() error = %v, want 系统错误透传", err)
		}
	}
	failing = false
	if _, err := service.Redeem(t.Context(), 1, "code"); err != nil {
		t.Fatalf("系统错误不应计入限流，Redeem() error = %v", err)
	}
}
