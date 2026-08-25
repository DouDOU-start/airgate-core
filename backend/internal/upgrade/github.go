package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// GithubRepo 仓库标识。
const GithubRepo = "DouDOU-start/airgate-core"

// githubClient 带 ETag 缓存和短时间内存缓存的 GitHub release 客户端。
type githubClient struct {
	repo string

	mu          sync.Mutex
	cached      *ReleaseInfo
	etag        string
	cacheExpire time.Time
}

func newGithubClient() *githubClient {
	return &githubClient{repo: GithubRepo}
}

// LatestRelease 拉取最新 release。10 分钟内复用内存缓存，命中后 0 网络请求；
// 缓存过期但有 ETag 时发送 If-None-Match，304 不消耗 GitHub 配额。
func (c *githubClient) LatestRelease(ctx context.Context) (*ReleaseInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cached != nil && time.Now().Before(c.cacheExpire) {
		return c.cached, nil
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", c.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if c.etag != "" {
		req.Header.Set("If-None-Match", c.etag)
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub API 失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified && c.cached != nil {
		c.cacheExpire = time.Now().Add(10 * time.Minute)
		return c.cached, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API 状态码 %d", resp.StatusCode)
	}

	var info ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("解析 release 失败: %w", err)
	}
	info.FetchedAt = time.Now()
	c.cached = &info
	c.etag = resp.Header.Get("ETag")
	c.cacheExpire = time.Now().Add(10 * time.Minute)
	return c.cached, nil
}

// Invalidate 强制让缓存失效，下次调用真实拉取。
func (c *githubClient) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cacheExpire = time.Time{}
}
