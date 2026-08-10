package pipeline

import (
	"net/http"
	"testing"
)

func TestAccountAuditTransportReusesDirectTransport(t *testing.T) {
	p := New(Options{})
	got, err := p.accountAuditTransport("")
	if err != nil {
		t.Fatal(err)
	}
	if got != p.accountDirectTransport {
		t.Fatal("账号直连流量没有复用 Pipeline Transport")
	}
}

func TestAccountAuditTransportCachesAndTunesProxy(t *testing.T) {
	p := New(Options{})
	first, err := p.accountAuditTransport("http://127.0.0.1:1080")
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.accountAuditTransport(" http://127.0.0.1:1080 ")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("代理 Transport 被重复构建，没有复用")
	}
	transport, ok := first.(*http.Transport)
	if !ok {
		t.Fatalf("Transport 类型 = %T", first)
	}
	if transport.MaxIdleConns != 2048 || transport.MaxIdleConnsPerHost != 512 || !transport.ForceAttemptHTTP2 {
		t.Fatalf("代理 Transport 未应用连接池配置：%+v", transport)
	}
}
