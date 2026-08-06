package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/config"
	"github.com/DouDOU-start/airgate-core/internal/pluginruntime"
)

func TestPluginHandlerUploadRealBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("短测试模式跳过真实插件进程构建")
	}
	gin.SetMode(gin.TestMode)

	buildDir := t.TempDir()
	binaryPath := filepath.Join(buildDir, "fixture-source")
	if runtime.GOOS == "windows" {
		binaryPath += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binaryPath, "../../pluginruntime/testdata/hookplugin")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("构建测试插件失败: %v\n%s", err, output)
	}

	binary, err := os.Open(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = binary.Close() }()

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(binaryPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(part, binary); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("config", "enabled: false\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	manager := pluginruntime.New(config.PluginsConfig{Dir: t.TempDir(), HookTimeoutMS: 500}, "error")
	t.Cleanup(func() { manager.StopAll(context.Background()) })
	handler := NewPluginHandler(manager)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/plugins/upload", body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	handler.UploadPlugin(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("上传接口状态码异常: %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var result struct {
		Code int `json:"code"`
		Data struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("解析上传响应失败: %v", err)
	}
	if result.Code != 0 || result.Data.ID != "fixture-hook" || result.Data.Enabled {
		t.Fatalf("上传响应异常: %+v", result)
	}
}
