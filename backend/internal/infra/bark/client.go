// Package bark 提供 Bark（https://github.com/Finb/Bark）推送客户端。
// 兼容官方 api.day.app 与自建 bark-server 的 POST /push JSON 接口。
package bark

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultServerURL = "https://api.day.app"
	maxBodyBytes     = 1 << 20
)

// Message 一条 Bark 推送。
type Message struct {
	Title    string
	Body     string
	URL      string
	Group    string
	Sound    string
	Level    string
	Subtitle string
}

// Client Bark HTTP 客户端。
type Client struct {
	httpClient *http.Client
}

// New 创建默认超时的 Bark 客户端。
func New() *Client {
	return &Client{httpClient: &http.Client{Timeout: 10 * time.Second}}
}

// Push 向指定 server 的单个 device key 发送推送。
// server 为空时使用官方 https://api.day.app。
func (c *Client) Push(ctx context.Context, server, deviceKey string, message Message) error {
	if c == nil {
		return errors.New("bark 客户端未初始化")
	}
	deviceKey = strings.TrimSpace(deviceKey)
	if deviceKey == "" {
		return errors.New("bark device key 未配置")
	}
	body := strings.TrimSpace(message.Body)
	if body == "" && strings.TrimSpace(message.Title) == "" {
		return errors.New("bark 推送标题与内容不能同时为空")
	}

	payload := map[string]any{
		"device_key": deviceKey,
		"body":       firstNonEmpty(body, strings.TrimSpace(message.Title)),
	}
	if title := strings.TrimSpace(message.Title); title != "" {
		payload["title"] = title
	}
	if subtitle := strings.TrimSpace(message.Subtitle); subtitle != "" {
		payload["subtitle"] = subtitle
	}
	if url := strings.TrimSpace(message.URL); url != "" {
		payload["url"] = url
	}
	if group := strings.TrimSpace(message.Group); group != "" {
		payload["group"] = group
	}
	if sound := strings.TrimSpace(message.Sound); sound != "" {
		payload["sound"] = sound
	}
	if level := strings.TrimSpace(message.Level); level != "" {
		payload["level"] = level
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化 Bark 请求失败：%w", err)
	}

	endpoint := pushURL(server)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("构造 Bark 请求失败：%w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("调用 Bark 服务失败：%w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("bark 服务返回 HTTP %d：%s", resp.StatusCode, truncate(string(respBody), 200))
	}

	// bark-server 成功时通常返回 {"code":200,...}；兼容纯文本 OK。
	var apiResp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err == nil && apiResp.Code != 0 && apiResp.Code != 200 {
		msg := strings.TrimSpace(apiResp.Message)
		if msg == "" {
			msg = truncate(string(respBody), 200)
		}
		return fmt.Errorf("bark 推送失败：code=%d，%s", apiResp.Code, msg)
	}
	return nil
}

// PushMany 向多个 device key 发送同一条消息；任一失败时聚合错误。
func (c *Client) PushMany(ctx context.Context, server string, deviceKeys []string, message Message) error {
	var sendErrors []error
	for _, key := range deviceKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if err := c.Push(ctx, server, key, message); err != nil {
			sendErrors = append(sendErrors, fmt.Errorf("key 尾号 %s：%w", keySuffix(key), err))
		}
	}
	if len(sendErrors) > 0 {
		return errors.Join(sendErrors...)
	}
	return nil
}

func pushURL(server string) string {
	server = strings.TrimSpace(server)
	if server == "" {
		server = defaultServerURL
	}
	server = strings.TrimRight(server, "/")
	return server + "/push"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func keySuffix(key string) string {
	runes := []rune(key)
	if len(runes) <= 6 {
		return key
	}
	return string(runes[len(runes)-6:])
}
