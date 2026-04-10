package account

import (
	"context"
	"testing"
	"time"
)

func TestImportIgnoresEnvironmentScopedIDs(t *testing.T) {
	service := NewService(stubRepository{
		create: func(_ context.Context, input CreateInput) (Account, error) {
			if len(input.GroupIDs) != 0 {
				t.Fatalf("expected import to clear group IDs, got %v", input.GroupIDs)
			}
			if input.ProxyID != nil {
				t.Fatalf("expected import to clear proxy ID, got %v", *input.ProxyID)
			}
			return Account{ID: 1, Name: input.Name}, nil
		},
	}, nil, nil)

	proxyID := int64(99)
	summary := service.Import(t.Context(), []CreateInput{{
		Name:           "demo",
		Platform:       "openai",
		Type:           "apikey",
		Credentials:    map[string]string{"api_key": "secret"},
		Priority:       3,
		MaxConcurrency: 5,
		RateMultiplier: 1.2,
		GroupIDs:       []int64{2, 1},
		ProxyID:        &proxyID,
	}})

	if summary.Imported != 1 || summary.Failed != 0 {
		t.Fatalf("unexpected import summary: %+v", summary)
	}
}

type stubRepository struct {
	create func(context.Context, CreateInput) (Account, error)
}

func (s stubRepository) List(context.Context, ListFilter) ([]Account, int64, error) {
	return nil, 0, nil
}

func (s stubRepository) ListAll(context.Context, ListFilter) ([]Account, error) {
	return nil, nil
}

func (s stubRepository) Create(ctx context.Context, input CreateInput) (Account, error) {
	if s.create == nil {
		return Account{}, nil
	}
	return s.create(ctx, input)
}

func (s stubRepository) Update(context.Context, int, UpdateInput) (Account, error) {
	return Account{}, nil
}

func (s stubRepository) Delete(context.Context, int) error { return nil }

func (s stubRepository) FindByID(context.Context, int, LoadOptions) (Account, error) {
	return Account{}, nil
}

func (s stubRepository) ListByPlatform(context.Context, string) ([]Account, error) {
	return nil, nil
}

func (s stubRepository) FindUsageLogs(context.Context, int, time.Time, time.Time) ([]UsageLog, error) {
	return nil, nil
}

func (s stubRepository) SaveCredentials(context.Context, int, map[string]string) error { return nil }

func (s stubRepository) MarkError(context.Context, int, string) error { return nil }
