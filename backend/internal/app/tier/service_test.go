package tier

import (
	"context"
	"errors"
	"testing"
)

// stubRepo 仅记录调用并回显输入，校验逻辑在 service 层测试。
type stubRepo struct {
	created CreateInput
	updated UpdateInput
}

func (r *stubRepo) List(context.Context, ListFilter) ([]Tier, int64, error) { return nil, 0, nil }
func (r *stubRepo) FindByID(context.Context, int) (Tier, error)             { return Tier{}, nil }
func (r *stubRepo) Create(_ context.Context, input CreateInput) (Tier, error) {
	r.created = input
	return Tier{ID: 1, Name: input.Name, Rates: input.Rates}, nil
}
func (r *stubRepo) Update(_ context.Context, id int, input UpdateInput) (Tier, error) {
	r.updated = input
	return Tier{ID: id}, nil
}
func (r *stubRepo) Delete(context.Context, int) error { return nil }

func TestServiceCreate_RatesValidation(t *testing.T) {
	tests := []struct {
		name    string
		rates   map[int64]float64
		wantErr error
	}{
		{name: "合法倍率通过", rates: map[int64]float64{1: 0.8, 2: 1.2}},
		{name: "空倍率表通过", rates: nil},
		{name: "零倍率拒绝", rates: map[int64]float64{1: 0}, wantErr: ErrInvalidRate},
		{name: "负倍率拒绝", rates: map[int64]float64{1: -0.5}, wantErr: ErrInvalidRate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&stubRepo{})
			_, err := svc.Create(context.Background(), CreateInput{Name: "vip", Rates: tt.rates})
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Create() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestServiceUpdate_RatesValidation(t *testing.T) {
	tests := []struct {
		name     string
		rates    map[int64]float64
		hasRates bool
		wantErr  error
	}{
		{name: "未提交 rates 不校验", hasRates: false},
		{name: "整体替换合法倍率", rates: map[int64]float64{3: 0.9}, hasRates: true},
		{name: "整体替换含零倍率拒绝", rates: map[int64]float64{3: 0}, hasRates: true, wantErr: ErrInvalidRate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&stubRepo{})
			_, err := svc.Update(context.Background(), 1, UpdateInput{Rates: tt.rates, HasRates: tt.hasRates})
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Update() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
