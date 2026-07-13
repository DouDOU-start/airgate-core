package invite

import (
	"context"
	"errors"
	"testing"
)

// stubRepository 表驱动测试用桩实现，未设置的字段调用会 panic（暴露非预期调用）。
type stubRepository struct {
	ensureProfile     func(context.Context, int) (Profile, error)
	getProfile        func(context.Context, int) (Profile, error)
	getByCode         func(context.Context, string) (Profile, error)
	bindInviter       func(context.Context, int, int) (bool, error)
	accrueRebate      func(context.Context, int, int, float64, string, string) (bool, error)
	transferToBalance func(context.Context, int) (float64, float64, error)
	setRateOverride   func(context.Context, int, *float64) error
}

func (s stubRepository) EnsureProfile(ctx context.Context, userID int) (Profile, error) {
	return s.ensureProfile(ctx, userID)
}
func (s stubRepository) GetProfile(ctx context.Context, userID int) (Profile, error) {
	return s.getProfile(ctx, userID)
}
func (s stubRepository) GetByCode(ctx context.Context, code string) (Profile, error) {
	return s.getByCode(ctx, code)
}
func (s stubRepository) BindInviter(ctx context.Context, userID, inviterID int) (bool, error) {
	return s.bindInviter(ctx, userID, inviterID)
}
func (s stubRepository) AccrueRebate(ctx context.Context, inviterID, sourceUserID int, amount float64, sourceOrderNo, idempotencyKey string) (bool, error) {
	return s.accrueRebate(ctx, inviterID, sourceUserID, amount, sourceOrderNo, idempotencyKey)
}
func (s stubRepository) TransferToBalance(ctx context.Context, userID int) (float64, float64, error) {
	return s.transferToBalance(ctx, userID)
}
func (s stubRepository) SetRateOverride(ctx context.Context, userID int, ratePercent *float64) error {
	return s.setRateOverride(ctx, userID, ratePercent)
}
func (s stubRepository) ListInvitees(context.Context, int, ListFilter) ([]InviteRelation, int64, error) {
	panic("not implemented")
}
func (s stubRepository) AdminListInvitees(context.Context, ListFilter) ([]InviteRelation, int64, error) {
	panic("not implemented")
}
func (s stubRepository) ListRebateLogs(context.Context, int, ListFilter) ([]RebateLog, int64, error) {
	panic("not implemented")
}
func (s stubRepository) AdminListRebateLogs(context.Context, ListFilter) ([]RebateLog, int64, error) {
	panic("not implemented")
}
func (s stubRepository) AdminListOverrides(context.Context, ListFilter) ([]OverrideEntry, int64, error) {
	panic("not implemented")
}

// stubSettings 固定返回一组配置项的 SettingsLister 桩实现。
type stubSettings struct {
	items []SettingItem
	err   error
}

func (s stubSettings) List(context.Context, string) ([]SettingItem, error) {
	return s.items, s.err
}

func enabledSettings(ratePercent string) stubSettings {
	return stubSettings{items: []SettingItem{
		{Key: settingKeyEnabled, Value: "true"},
		{Key: settingKeyRebateRate, Value: ratePercent},
	}}
}

func TestIsEnabled(t *testing.T) {
	tests := []struct {
		name string
		sl   SettingsLister
		want bool
	}{
		{"未注入 settings", nil, false},
		{"开关开启", enabledSettings("5"), true},
		{"开关缺失", stubSettings{items: []SettingItem{{Key: settingKeyRebateRate, Value: "5"}}}, false},
		{"开关显式关闭", stubSettings{items: []SettingItem{{Key: settingKeyEnabled, Value: "false"}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewService(stubRepository{})
			if tt.sl != nil {
				s.SetSettingsLister(tt.sl)
			}
			if got := s.IsEnabled(t.Context()); got != tt.want {
				t.Fatalf("IsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClampRate(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want float64
	}{
		{"负数截断为 0", -5, 0},
		{"超过 100 截断为 100", 150, 100},
		{"正常范围内不变", 8.5, 8.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampRate(tt.in); got != tt.want {
				t.Fatalf("clampRate(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestResolveRatePercentPrefersOverride(t *testing.T) {
	s := NewService(stubRepository{})
	s.SetSettingsLister(enabledSettings("5"))

	override := 20.0
	got := s.resolveRatePercent(t.Context(), Profile{RebateRateOverride: &override})
	if got != 20 {
		t.Fatalf("resolveRatePercent() with override = %v, want 20", got)
	}

	got = s.resolveRatePercent(t.Context(), Profile{})
	if got != 5 {
		t.Fatalf("resolveRatePercent() without override = %v, want global 5", got)
	}
}

func TestBindInviterSkipsWhenDisabled(t *testing.T) {
	called := false
	s := NewService(stubRepository{
		ensureProfile: func(context.Context, int) (Profile, error) {
			called = true
			return Profile{}, nil
		},
	})
	if err := s.BindInviter(t.Context(), 1, "ABC"); err != nil {
		t.Fatalf("BindInviter() error = %v, want nil", err)
	}
	if called {
		t.Fatal("BindInviter() should not touch repo when feature disabled")
	}
}

func TestBindInviterRejectsSelfInvite(t *testing.T) {
	s := NewService(stubRepository{
		ensureProfile: func(_ context.Context, userID int) (Profile, error) {
			return Profile{UserID: userID}, nil
		},
		getByCode: func(context.Context, string) (Profile, error) {
			return Profile{UserID: 1}, nil // 自己的画像
		},
	})
	s.SetSettingsLister(enabledSettings("5"))

	if err := s.BindInviter(t.Context(), 1, "MYCODE"); !errors.Is(err, ErrSelfInvite) {
		t.Fatalf("BindInviter() error = %v, want ErrSelfInvite", err)
	}
}

func TestBindInviterRejectsAlreadyBound(t *testing.T) {
	inviterID := 2
	s := NewService(stubRepository{
		ensureProfile: func(_ context.Context, userID int) (Profile, error) {
			return Profile{UserID: userID, InviterID: &inviterID}, nil
		},
	})
	s.SetSettingsLister(enabledSettings("5"))

	if err := s.BindInviter(t.Context(), 1, "SOMECODE"); !errors.Is(err, ErrAlreadyBound) {
		t.Fatalf("BindInviter() error = %v, want ErrAlreadyBound", err)
	}
}

func TestBindInviterRejectsInvalidCode(t *testing.T) {
	s := NewService(stubRepository{
		ensureProfile: func(_ context.Context, userID int) (Profile, error) {
			return Profile{UserID: userID}, nil
		},
		getByCode: func(context.Context, string) (Profile, error) {
			return Profile{}, ErrProfileNotFound
		},
	})
	s.SetSettingsLister(enabledSettings("5"))

	if err := s.BindInviter(t.Context(), 1, "NOPE"); !errors.Is(err, ErrCodeInvalid) {
		t.Fatalf("BindInviter() error = %v, want ErrCodeInvalid", err)
	}
}

func TestBindInviterSucceeds(t *testing.T) {
	var boundUserID, boundInviterID int
	s := NewService(stubRepository{
		ensureProfile: func(_ context.Context, userID int) (Profile, error) {
			return Profile{UserID: userID}, nil
		},
		getByCode: func(context.Context, string) (Profile, error) {
			return Profile{UserID: 2}, nil
		},
		bindInviter: func(_ context.Context, userID, inviterID int) (bool, error) {
			boundUserID, boundInviterID = userID, inviterID
			return true, nil
		},
	})
	s.SetSettingsLister(enabledSettings("5"))

	if err := s.BindInviter(t.Context(), 1, "friendcode"); err != nil {
		t.Fatalf("BindInviter() error = %v, want nil", err)
	}
	if boundUserID != 1 || boundInviterID != 2 {
		t.Fatalf("BindInviter() bound (%d, %d), want (1, 2)", boundUserID, boundInviterID)
	}
}

func TestAccrueRebateForPaymentSkipsWithoutInviter(t *testing.T) {
	s := NewService(stubRepository{
		getProfile: func(context.Context, int) (Profile, error) {
			return Profile{}, nil // 无邀请人
		},
	})
	s.SetSettingsLister(enabledSettings("5"))

	rebate, err := s.AccrueRebateForPayment(t.Context(), 1, 100, "ORDER1")
	if err != nil {
		t.Fatalf("AccrueRebateForPayment() error = %v", err)
	}
	if rebate != 0 {
		t.Fatalf("AccrueRebateForPayment() = %v, want 0", rebate)
	}
}

func TestAccrueRebateForPaymentCalculatesAndPassesIdempotencyKey(t *testing.T) {
	inviterID := 2
	var gotInviterID, gotSourceUserID int
	var gotAmount float64
	var gotOrderNo, gotKey string

	s := NewService(stubRepository{
		getProfile: func(_ context.Context, userID int) (Profile, error) {
			if userID == 1 {
				return Profile{UserID: 1, InviterID: &inviterID}, nil
			}
			return Profile{UserID: 2}, nil
		},
		accrueRebate: func(_ context.Context, inviterIDArg, sourceUserID int, amount float64, orderNo, key string) (bool, error) {
			gotInviterID, gotSourceUserID, gotAmount, gotOrderNo, gotKey = inviterIDArg, sourceUserID, amount, orderNo, key
			return true, nil
		},
	})
	s.SetSettingsLister(enabledSettings("5"))

	rebate, err := s.AccrueRebateForPayment(t.Context(), 1, 100, "ORDER1")
	if err != nil {
		t.Fatalf("AccrueRebateForPayment() error = %v", err)
	}
	if rebate != 5 {
		t.Fatalf("AccrueRebateForPayment() rebate = %v, want 5 (5%% of 100)", rebate)
	}
	if gotInviterID != 2 || gotSourceUserID != 1 || gotAmount != 5 {
		t.Fatalf("AccrueRebate() called with inviter=%d source=%d amount=%v, want inviter=2 source=1 amount=5", gotInviterID, gotSourceUserID, gotAmount)
	}
	if gotOrderNo != "ORDER1" {
		t.Fatalf("AccrueRebate() orderNo = %q, want ORDER1", gotOrderNo)
	}
	if gotKey != "invite-accrue:ORDER1" {
		t.Fatalf("AccrueRebate() idempotencyKey = %q, want invite-accrue:ORDER1", gotKey)
	}
}

func TestAccrueRebateForPaymentSkipsWhenDisabled(t *testing.T) {
	called := false
	s := NewService(stubRepository{
		getProfile: func(context.Context, int) (Profile, error) {
			called = true
			return Profile{}, nil
		},
	})
	rebate, err := s.AccrueRebateForPayment(t.Context(), 1, 100, "ORDER1")
	if err != nil || rebate != 0 {
		t.Fatalf("AccrueRebateForPayment() = (%v, %v), want (0, nil)", rebate, err)
	}
	if called {
		t.Fatal("AccrueRebateForPayment() should not touch repo when feature disabled")
	}
}

func TestAccrueRebateForPaymentRejectsInvalidAmount(t *testing.T) {
	s := NewService(stubRepository{})
	s.SetSettingsLister(enabledSettings("5"))

	for _, amount := range []float64{0, -1} {
		rebate, err := s.AccrueRebateForPayment(t.Context(), 1, amount, "ORDER1")
		if err != nil || rebate != 0 {
			t.Fatalf("AccrueRebateForPayment(amount=%v) = (%v, %v), want (0, nil)", amount, rebate, err)
		}
	}
}
