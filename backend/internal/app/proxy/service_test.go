package proxy

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
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

func TestCreateUpdateDeleteDelegateToRepository(t *testing.T) {
	service := NewService(proxyStubRepository{
		create: func(_ context.Context, input CreateInput) (Proxy, error) {
			if input.Name != "p1" {
				t.Fatalf("创建输入异常: %+v", input)
			}
			return Proxy{ID: 1, Name: input.Name}, nil
		},
		update: func(_ context.Context, id int, input UpdateInput) (Proxy, error) {
			if id != 1 || input.Name == nil || *input.Name != "p2" {
				t.Fatalf("更新输入异常: id=%d input=%+v", id, input)
			}
			return Proxy{ID: id, Name: *input.Name}, nil
		},
		delete: func(_ context.Context, id int) error {
			if id != 1 {
				t.Fatalf("删除 ID = %d，期望 1", id)
			}
			return nil
		},
	})

	created, err := service.Create(t.Context(), CreateInput{Name: "p1"})
	if err != nil || created.ID != 1 {
		t.Fatalf("创建结果异常: %+v, %v", created, err)
	}
	name := "p2"
	updated, err := service.Update(t.Context(), 1, UpdateInput{Name: &name})
	if err != nil || updated.Name != "p2" {
		t.Fatalf("更新结果异常: %+v, %v", updated, err)
	}
	if err := service.Delete(t.Context(), 1); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
}

func TestBuildProxyTransportForHTTPProxyWithAuth(t *testing.T) {
	transport, err := buildProxyTransport(Proxy{
		Protocol: "http",
		Address:  "127.0.0.1",
		Port:     8080,
		Username: "user",
		Password: "pass",
	})
	if err != nil {
		t.Fatalf("构建 HTTP 代理失败: %v", err)
	}
	if transport.Proxy == nil {
		t.Fatal("HTTP 代理应设置 Proxy 函数")
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	proxyURL, err := transport.Proxy(req)
	if err != nil {
		t.Fatalf("获取代理 URL 失败: %v", err)
	}
	if proxyURL.String() != "http://user:pass@127.0.0.1:8080" {
		t.Fatalf("代理 URL = %q，期望带认证信息", proxyURL.String())
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	if got := transport.ProxyConnectHeader.Get("Proxy-Authorization"); got != wantAuth {
		t.Fatalf("代理认证头 = %q，期望 %q", got, wantAuth)
	}
}

func TestBuildProxyTransportRejectsUnsupportedProtocol(t *testing.T) {
	_, err := buildProxyTransport(Proxy{Protocol: "ftp", Address: "127.0.0.1", Port: 21})
	if err == nil {
		t.Fatal("不支持的代理协议应返回错误")
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
