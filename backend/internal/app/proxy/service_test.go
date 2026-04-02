package proxy

import (
	"context"
	"errors"
	"testing"
)

func TestListNormalizesPagination(t *testing.T) {
	var captured ListFilter
	service := NewService(proxyStubRepository{
		list: func(_ context.Context, filter ListFilter) ([]Proxy, int64, error) {
			captured = filter
			return nil, 0, nil
		},
	})

	result, err := service.List(t.Context(), ListFilter{})
	if err != nil {
		t.Fatalf("List() returned error: %v", err)
	}
	if captured.Page != 1 || captured.PageSize != 20 {
		t.Fatalf("List() normalized filter = %+v, want page=1 pageSize=20", captured)
	}
	if result.Page != 1 || result.PageSize != 20 {
		t.Fatalf("List() result pagination = %+v, want page=1 pageSize=20", result)
	}
}

func TestTestReturnsNotFoundError(t *testing.T) {
	service := NewService(proxyStubRepository{
		findByID: func(_ context.Context, _ int) (Proxy, error) {
			return Proxy{}, ErrProxyNotFound
		},
	})

	_, err := service.Test(t.Context(), 7)
	if !errors.Is(err, ErrProxyNotFound) {
		t.Fatalf("Test() error = %v, want ErrProxyNotFound", err)
	}
}

func TestTestUsesConfiguredProber(t *testing.T) {
	service := NewService(proxyStubRepository{
		findByID: func(_ context.Context, id int) (Proxy, error) {
			return Proxy{ID: id, Name: "p1"}, nil
		},
	})
	service.prober = stubProber{
		probe: func(_ context.Context, item Proxy) TestResult {
			if item.ID != 9 {
				t.Fatalf("prober got proxy id=%d, want 9", item.ID)
			}
			return TestResult{Success: true, Latency: 12}
		},
	}

	result, err := service.Test(t.Context(), 9)
	if err != nil {
		t.Fatalf("Test() returned error: %v", err)
	}
	if !result.Success || result.Latency != 12 {
		t.Fatalf("Test() result = %+v, want success latency=12", result)
	}
}

type stubProber struct {
	probe func(context.Context, Proxy) TestResult
}

func (s stubProber) Probe(ctx context.Context, p Proxy) TestResult {
	return s.probe(ctx, p)
}

type proxyStubRepository struct {
	list     func(context.Context, ListFilter) ([]Proxy, int64, error)
	findByID func(context.Context, int) (Proxy, error)
	create   func(context.Context, CreateInput) (Proxy, error)
	update   func(context.Context, int, UpdateInput) (Proxy, error)
	delete   func(context.Context, int) error
}

func (s proxyStubRepository) List(ctx context.Context, filter ListFilter) ([]Proxy, int64, error) {
	if s.list == nil {
		return nil, 0, nil
	}
	return s.list(ctx, filter)
}

func (s proxyStubRepository) FindByID(ctx context.Context, id int) (Proxy, error) {
	if s.findByID == nil {
		return Proxy{}, nil
	}
	return s.findByID(ctx, id)
}

func (s proxyStubRepository) Create(ctx context.Context, input CreateInput) (Proxy, error) {
	if s.create == nil {
		return Proxy{}, nil
	}
	return s.create(ctx, input)
}

func (s proxyStubRepository) Update(ctx context.Context, id int, input UpdateInput) (Proxy, error) {
	if s.update == nil {
		return Proxy{}, nil
	}
	return s.update(ctx, id, input)
}

func (s proxyStubRepository) Delete(ctx context.Context, id int) error {
	if s.delete == nil {
		return nil
	}
	return s.delete(ctx, id)
}
