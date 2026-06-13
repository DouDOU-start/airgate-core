package proxy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	xproxy "golang.org/x/net/proxy"

	"github.com/DouDOU-start/airgate-core/internal/pkg/pagination"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// Service 提供代理域用例编排。
type Service struct {
	repo   Repository
	prober Prober
}

// Prober 定义代理探测能力。
type Prober interface {
	Probe(context.Context, Proxy) TestResult
}

// NewService 创建代理服务。
func NewService(repo Repository) *Service {
	return &Service{
		repo:   repo,
		prober: DefaultProber{},
	}
}

// List 查询代理列表。
func (s *Service) List(ctx context.Context, filter ListFilter) (ListResult, error) {
	page, pageSize := pagination.Normalize(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.List(ctx, filter)
	if err != nil {
		return ListResult{}, err
	}
	return ListResult{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// Create 创建代理。
func (s *Service) Create(ctx context.Context, input CreateInput) (Proxy, error) {
	logger := sdk.LoggerFromContext(ctx)
	p, err := s.repo.Create(ctx, input)
	if err != nil {
		// 不打印 username/password；只保留协议与地址作为定位线索。
		logger.Error("proxy_config_persist_failed",
			"op", "create",
			"name", input.Name,
			"protocol", input.Protocol,
			"address", input.Address,
			sdk.LogFieldError, err)
		return p, err
	}
	logger.Info("proxy_config_created",
		"proxy_id", p.ID,
		"name", p.Name,
		"protocol", p.Protocol,
		"address", p.Address)
	return p, nil
}

// Update 更新代理。
func (s *Service) Update(ctx context.Context, id int, input UpdateInput) (Proxy, error) {
	logger := sdk.LoggerFromContext(ctx)
	p, err := s.repo.Update(ctx, id, input)
	if err != nil {
		logger.Error("proxy_config_persist_failed",
			"op", "update",
			"proxy_id", id,
			sdk.LogFieldError, err)
		return p, err
	}
	return p, nil
}

// Delete 删除代理。
func (s *Service) Delete(ctx context.Context, id int) error {
	logger := sdk.LoggerFromContext(ctx)
	if err := s.repo.Delete(ctx, id); err != nil {
		logger.Error("proxy_config_persist_failed",
			"op", "delete",
			"proxy_id", id,
			sdk.LogFieldError, err)
		return err
	}
	logger.Info("proxy_config_deleted", "proxy_id", id)
	return nil
}

// Test 测试代理连通性。
func (s *Service) Test(ctx context.Context, id int) (TestResult, error) {
	logger := sdk.LoggerFromContext(ctx)
	item, err := s.repo.FindByID(ctx, id)
	if err != nil {
		logger.Error("proxy_config_persist_failed",
			"op", "find_by_id",
			"proxy_id", id,
			sdk.LogFieldError, err)
		return TestResult{}, err
	}
	result := s.prober.Probe(ctx, item)
	if !result.Success {
		logger.Warn("proxy_test_failed",
			"proxy_id", id,
			"protocol", item.Protocol,
			"address", item.Address,
			sdk.LogFieldReason, result.ErrorMsg)
	}
	return result, nil
}

// DefaultProber 是默认代理探测器。
type DefaultProber struct{}

// Probe 通过代理发起探测请求并返回结果。
func (DefaultProber) Probe(ctx context.Context, p Proxy) TestResult {
	const timeout = 15 * time.Second

	transport, err := buildProxyTransport(p)
	if err != nil {
		return TestResult{Success: false, ErrorMsg: "构建代理传输失败: " + err.Error()}
	}
	client := &http.Client{Transport: transport, Timeout: timeout}

	type probeEndpoint struct {
		url   string
		parse func([]byte) (ip, country, countryCode, city string)
	}

	endpoints := []probeEndpoint{
		{
			url: "http://ip-api.com/json/?lang=zh-CN",
			parse: func(body []byte) (string, string, string, string) {
				var r struct {
					Status      string `json:"status"`
					Query       string `json:"query"`
					Country     string `json:"country"`
					CountryCode string `json:"countryCode"`
					City        string `json:"city"`
				}
				if json.Unmarshal(body, &r) != nil || r.Status != "success" {
					return "", "", "", ""
				}
				return r.Query, r.Country, r.CountryCode, r.City
			},
		},
		{
			url: "http://httpbin.org/ip",
			parse: func(body []byte) (string, string, string, string) {
				var r struct {
					Origin string `json:"origin"`
				}
				if json.Unmarshal(body, &r) != nil {
					return "", "", "", ""
				}
				return r.Origin, "", "", ""
			},
		},
	}

	var lastErr string
	for _, ep := range endpoints {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, ep.url, nil)
		if reqErr != nil {
			lastErr = fmt.Sprintf("[%s] 创建请求失败: %v", ep.url, reqErr)
			continue
		}

		start := time.Now()
		resp, doErr := client.Do(req)
		latency := time.Since(start).Milliseconds()
		if doErr != nil {
			lastErr = fmt.Sprintf("[%s] 请求失败: %v", ep.url, doErr)
			slog.Warn("proxy_probe_endpoint_failed", "url", ep.url, sdk.LogFieldError, doErr)
			continue
		}

		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Sprintf("[%s] HTTP %d", ep.url, resp.StatusCode)
			continue
		}

		ip, country, countryCode, city := ep.parse(body)
		if ip == "" {
			lastErr = fmt.Sprintf("[%s] 解析响应失败", ep.url)
			continue
		}

		return TestResult{
			Success:     true,
			Latency:     latency,
			IPAddress:   ip,
			Country:     country,
			CountryCode: countryCode,
			City:        city,
		}
	}

	for _, target := range []string{"https://api.openai.com", "https://api.anthropic.com"} {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
		if reqErr != nil {
			continue
		}
		start := time.Now()
		resp, doErr := client.Do(req)
		latency := time.Since(start).Milliseconds()
		if doErr != nil {
			continue
		}
		_ = resp.Body.Close()
		return TestResult{
			Success: true,
			Latency: latency,
		}
	}

	return TestResult{Success: false, ErrorMsg: lastErr}
}

func buildProxyTransport(p Proxy) (*http.Transport, error) {
	addr := net.JoinHostPort(p.Address, strconv.Itoa(p.Port))

	switch p.Protocol {
	case "http":
		proxyURL := &url.URL{
			Scheme: "http",
			Host:   addr,
		}
		transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		if p.Username != "" {
			proxyURL.User = url.UserPassword(p.Username, p.Password)
			basicAuth := base64.StdEncoding.EncodeToString([]byte(p.Username + ":" + p.Password))
			transport.ProxyConnectHeader = http.Header{
				"Proxy-Authorization": {"Basic " + basicAuth},
			}
		}
		return transport, nil
	case "socks5":
		var auth *xproxy.Auth
		if p.Username != "" {
			auth = &xproxy.Auth{
				User:     p.Username,
				Password: p.Password,
			}
		}
		dialer, err := xproxy.SOCKS5("tcp", addr, auth, xproxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("创建 SOCKS5 dialer 失败: %w", err)
		}
		return &http.Transport{
			DialContext: func(_ context.Context, network, address string) (net.Conn, error) {
				return dialer.Dial(network, address)
			},
		}, nil
	default:
		return nil, fmt.Errorf("不支持的代理协议: %s", p.Protocol)
	}
}
