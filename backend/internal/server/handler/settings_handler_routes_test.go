package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
)

// stubSettingsRepo settings 仓储桩：返回固定数据并捕获写入。
type stubSettingsRepo struct {
	items    []appsettings.Setting
	upserted []appsettings.ItemInput
}

func (s *stubSettingsRepo) List(_ context.Context, group string) ([]appsettings.Setting, error) {
	if group == "" {
		return s.items, nil
	}
	out := make([]appsettings.Setting, 0)
	for _, item := range s.items {
		if item.Group == group {
			out = append(out, item)
		}
	}
	return out, nil
}

func (s *stubSettingsRepo) UpsertMany(_ context.Context, items []appsettings.ItemInput) error {
	s.upserted = append(s.upserted, items...)
	return nil
}

func newSettingsTestRouter(repo *stubSettingsRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewSettingsHandler(appsettings.NewService(repo, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"))
	router := gin.New()
	router.GET("/settings", handler.GetSettings)
	router.PUT("/settings", handler.UpdateSettings)
	router.POST("/settings/upload", handler.UploadFile)
	router.GET("/settings/admin-api-key", handler.GetAdminAPIKey)
	return router
}

// TestGetAdminAPIKeyEmptyDataIsExplicitNull 管理员 API Key 未生成时，响应体必须带
// 显式的 "data":null，而不是把 data 字段整个省略——前端 useQuery 的 queryFn 不允许
// 返回 undefined，键缺失会被 react-query 当成查询失败抛错（回归用例，对应 R.Data 曾经
// 带 omitempty 导致 nil 被丢字段的 bug）。
func TestGetAdminAPIKeyEmptyDataIsExplicitNull(t *testing.T) {
	router := newSettingsTestRouter(&stubSettingsRepo{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/settings/admin-api-key", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GetAdminAPIKey status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	dataRaw, ok := raw["data"]
	if !ok {
		t.Fatalf("response missing \"data\" key entirely, body = %s", rec.Body.String())
	}
	if string(dataRaw) != "null" {
		t.Fatalf("data = %s, want null", dataRaw)
	}
}

// TestGetSettingsFiltersSensitive 通用设置端点：security 组整组不回显、
// smtp_password 已配置时掩码为哨兵值（防止通用端点绕过 GetAdminAPIKey 的脱敏设计）。
func TestGetSettingsFiltersSensitive(t *testing.T) {
	repo := &stubSettingsRepo{items: []appsettings.Setting{
		{Key: "site_name", Value: "AirGate", Group: "site"},
		{Key: "smtp_host", Value: "smtp.example.com", Group: "smtp"},
		{Key: "smtp_password", Value: "super-secret", Group: "smtp"},
		{Key: "admin_api_key_hash", Value: "hash-value", Group: "security"},
		{Key: "admin_api_key_encrypted", Value: "cipher-value", Group: "security"},
		{Key: "admin_api_key_hint", Value: "admin-...abcd", Group: "security"},
	}}
	router := newSettingsTestRouter(repo)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GetSettings status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Data []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
			Group string `json:"group"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	got := map[string]string{}
	for _, item := range resp.Data {
		if item.Group == "security" {
			t.Fatalf("security 组设置不应出现在通用端点响应: %+v", item)
		}
		got[item.Key] = item.Value
	}
	if body := rec.Body.String(); strings.Contains(body, "hash-value") || strings.Contains(body, "cipher-value") || strings.Contains(body, "super-secret") {
		t.Fatalf("响应泄漏敏感值: %s", body)
	}
	if v, ok := got["smtp_password"]; !ok || v != appsettings.MaskedValue {
		t.Fatalf("smtp_password 应掩码为哨兵值 %q，got %q (present=%v)", appsettings.MaskedValue, v, ok)
	}
	if got["smtp_host"] != "smtp.example.com" || got["site_name"] != "AirGate" {
		t.Fatalf("非敏感设置应原样返回: %v", got)
	}
}

// TestUpdateSettingsKeepsMaskedPassword 掩码键传哨兵值 = 保持库中现值（跳过写入）；
// 传空串 = 真实清空；传新值 = 正常落库。
func TestUpdateSettingsKeepsMaskedPassword(t *testing.T) {
	repo := &stubSettingsRepo{}
	router := newSettingsTestRouter(repo)

	body := `{"settings":[
		{"key":"smtp_password","value":"` + appsettings.MaskedValue + `","group":"smtp"},
		{"key":"smtp_host","value":"smtp.new.com","group":"smtp"}
	]}`
	req := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("UpdateSettings status = %d, body = %s", rec.Code, rec.Body.String())
	}

	for _, item := range repo.upserted {
		if item.Key == "smtp_password" {
			t.Fatalf("掩码哨兵 smtp_password 不应落库: %+v", item)
		}
	}
	found := false
	for _, item := range repo.upserted {
		if item.Key == "smtp_host" && item.Value == "smtp.new.com" {
			found = true
		}
	}
	if !found {
		t.Fatalf("smtp_host 更新应落库, got %+v", repo.upserted)
	}

	// 传空串 = 真实清空（恢复清空能力）
	repo.upserted = nil
	body = `{"settings":[{"key":"smtp_password","value":"","group":"smtp"}]}`
	req = httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("UpdateSettings status = %d", rec.Code)
	}
	if len(repo.upserted) != 1 || repo.upserted[0].Key != "smtp_password" || repo.upserted[0].Value != "" {
		t.Fatalf("空串应落库清空密码, got %+v", repo.upserted)
	}

	// 传了新密码则正常落库
	repo.upserted = nil
	body = `{"settings":[{"key":"smtp_password","value":"new-pass","group":"smtp"}]}`
	req = httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("UpdateSettings status = %d", rec.Code)
	}
	if len(repo.upserted) != 1 || repo.upserted[0].Value != "new-pass" {
		t.Fatalf("新密码应落库, got %+v", repo.upserted)
	}
}

// TestUpdateSettingsRejectsSecurityKeys security 组键（含"换 group 写同名 key"绕过）
// 经通用更新端点一律 400 拒写。
func TestUpdateSettingsRejectsSecurityKeys(t *testing.T) {
	repo := &stubSettingsRepo{}
	router := newSettingsTestRouter(repo)

	for _, body := range []string{
		`{"settings":[{"key":"admin_api_key_hash","value":"evil","group":"security"}]}`,
		`{"settings":[{"key":"admin_api_key_hash","value":"evil","group":"site"}]}`,
	} {
		req := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("security 键写入应 400, got %d, body=%s", rec.Code, rec.Body.String())
		}
		if len(repo.upserted) != 0 {
			t.Fatalf("security 键不应落库: %+v", repo.upserted)
		}
	}
}

func multipartUpload(t *testing.T, filename string, size int) (*bytes.Buffer, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	writer := multipart.NewWriter(buf)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(bytes.Repeat([]byte{0xAB}, size)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return buf, writer.FormDataContentType()
}

// TestUploadFileValidation 上传校验：拒绝 .svg（存储型 XSS 载体）与超大文件，
// 放行位图并生成 UUID 文件名。
func TestUploadFileValidation(t *testing.T) {
	t.Chdir(t.TempDir()) // UploadFile 落盘到相对路径 data/uploads，隔离到临时目录

	router := newSettingsTestRouter(&stubSettingsRepo{})

	cases := []struct {
		name     string
		filename string
		size     int
		wantCode int
	}{
		{"png_ok", "logo.png", 128, http.StatusOK},
		{"svg_rejected", "logo.svg", 128, http.StatusBadRequest},
		{"exe_rejected", "logo.exe", 128, http.StatusBadRequest},
		{"oversize_rejected", "big.png", 2<<20 + 1, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, contentType := multipartUpload(t, tc.filename, tc.size)
			req := httptest.NewRequest(http.MethodPost, "/settings/upload", body)
			req.Header.Set("Content-Type", contentType)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Fatalf("UploadFile(%s) status = %d, want %d; body=%s", tc.filename, rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantCode == http.StatusOK {
				var resp struct {
					Data struct {
						URL string `json:"url"`
					} `json:"data"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("解析响应失败: %v", err)
				}
				if !strings.HasPrefix(resp.Data.URL, "/uploads/") || !strings.HasSuffix(resp.Data.URL, ".png") {
					t.Fatalf("上传 URL 形态异常: %q", resp.Data.URL)
				}
				// UUID 文件名：36 字符（8-4-4-4-12）
				name := strings.TrimSuffix(strings.TrimPrefix(resp.Data.URL, "/uploads/"), ".png")
				if len(name) != 36 || strings.Count(name, "-") != 4 {
					t.Fatalf("文件名应为 UUID，got %q", name)
				}
			}
		})
	}
}
