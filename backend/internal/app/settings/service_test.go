package settings

import (
	"context"
	"testing"
)

func TestUpdateClonesInput(t *testing.T) {
	var captured []ItemInput
	service := NewService(settingsStubRepository{
		upsertMany: func(_ context.Context, items []ItemInput) error {
			captured = append(captured, items...)
			return nil
		},
	})

	input := []ItemInput{{Key: "site_name", Value: "Airgate"}}
	if err := service.Update(t.Context(), input); err != nil {
		t.Fatalf("Update() returned error: %v", err)
	}

	input[0].Value = "Changed"
	if captured[0].Value != "Airgate" {
		t.Fatalf("captured value = %q, want Airgate", captured[0].Value)
	}
}

type settingsStubRepository struct {
	list       func(context.Context, string) ([]Setting, error)
	upsertMany func(context.Context, []ItemInput) error
}

func (s settingsStubRepository) List(ctx context.Context, group string) ([]Setting, error) {
	if s.list == nil {
		return nil, nil
	}
	return s.list(ctx, group)
}

func (s settingsStubRepository) UpsertMany(ctx context.Context, items []ItemInput) error {
	if s.upsertMany == nil {
		return nil
	}
	return s.upsertMany(ctx, items)
}
