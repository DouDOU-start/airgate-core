package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDetectModeReturnsKnownMode(t *testing.T) {
	got := DetectMode()
	if got != ModeDocker && got != ModeSystemd && got != ModeNoop {
		t.Fatalf("部署模式 = %q，期望已知模式", got)
	}
}

func TestIsWritableAndCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	dst := filepath.Join(dir, "target")
	if err := os.WriteFile(src, []byte("airgate"), 0o600); err != nil {
		t.Fatalf("写入源文件失败: %v", err)
	}

	if !isWritable(src) {
		t.Fatal("临时文件应可写")
	}
	if isWritable(filepath.Join(dir, "missing", "file")) {
		t.Fatal("不存在父目录的文件不应可写")
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("复制文件失败: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("读取目标文件失败: %v", err)
	}
	if string(data) != "airgate" {
		t.Fatalf("复制内容 = %q，期望 airgate", string(data))
	}
}

func TestPickAssetAndIsNewer(t *testing.T) {
	assets := []Asset{
		{Name: "airgate-core-linux-amd64", DownloadURL: "https://example.test/linux"},
		{Name: "airgate-core-darwin-arm64", DownloadURL: "https://example.test/darwin"},
	}
	asset, err := pickAsset(assets, "linux", "amd64")
	if err != nil {
		t.Fatalf("选择资产失败: %v", err)
	}
	if asset.DownloadURL != "https://example.test/linux" {
		t.Fatalf("资产 URL = %q，期望 linux 资产", asset.DownloadURL)
	}
	if _, err := pickAsset(assets, "windows", "amd64"); err == nil {
		t.Fatal("缺少匹配资产时应返回错误")
	}

	if !isNewer("v1.2.0", "v1.1.0") || !isNewer("v1.2.0", "dev") || !isNewer("v1.2.0", "v1.1.0-dirty") {
		t.Fatal("新版本判断应识别更新场景")
	}
	if isNewer("", "v1.1.0") || isNewer("v1.2.0", "v1.2.0") {
		t.Fatal("新版本判断误判")
	}
}

func TestStatusBoxStoreLoadAndUpdate(t *testing.T) {
	box := &statusBox{}
	box.store(Status{State: StateDownloading, Progress: 0.2})
	box.update(func(st *Status) {
		st.Progress = 0.8
		st.Message = "即将完成"
	})

	got := box.load()
	if got.State != StateDownloading || got.Progress != 0.8 || got.Message != "即将完成" {
		t.Fatalf("状态盒结果异常: %+v", got)
	}
}

func TestServiceModeStatusAndRunPrechecks(t *testing.T) {
	service := NewService(ModeDocker, nil)
	if service.Mode() != ModeDocker {
		t.Fatalf("模式 = %q，期望 docker", service.Mode())
	}
	if service.Status().State != StateIdle {
		t.Fatalf("初始状态 = %+v，期望 idle", service.Status())
	}
	if err := service.Run(context.Background(), RunRequest{ConfirmDBBackup: true}); err == nil {
		t.Fatal("非 systemd 模式不应允许一键升级")
	}

	service = NewService(ModeSystemd, nil)
	if err := service.Run(context.Background(), RunRequest{}); err == nil || err.Error() != "请先确认已备份数据库（confirm_db_backup=true）" {
		t.Fatalf("缺少备份确认错误 = %v", err)
	}
}

func TestInfoUsesCachedReleaseAndDockerInstructions(t *testing.T) {
	service := NewService(ModeDocker, nil)
	now := time.Now()
	service.github = &githubClient{
		cached: &ReleaseInfo{
			TagName:   "v999.0.0",
			HTMLURL:   "https://example.test/release",
			Body:      "发布说明",
			FetchedAt: now,
		},
		cacheExpire: now.Add(time.Hour),
	}

	info, err := service.Info(context.Background())
	if err != nil {
		t.Fatalf("获取升级信息失败: %v", err)
	}
	if info.Mode != ModeDocker || !info.HasUpdate || info.CanUpgrade || info.Instructions == "" || info.CheckedAt == nil {
		t.Fatalf("升级信息异常: %+v", info)
	}
}

func TestDownloadUpdatesProgress(t *testing.T) {
	body := []byte("new-binary")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	service := NewService(ModeSystemd, nil)
	dst := filepath.Join(t.TempDir(), "airgate-core.new")
	err := service.download(&Asset{Name: "airgate-core-linux-amd64", DownloadURL: server.URL, Size: int64(len(body))}, dst)
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("读取下载文件失败: %v", err)
	}
	if string(data) != string(body) {
		t.Fatalf("下载内容 = %q，期望 %q", string(data), string(body))
	}
	if service.Status().Progress != 1 {
		t.Fatalf("下载进度 = %v，期望 1", service.Status().Progress)
	}
}

func TestVerifyChecksumUsesReleaseChecksumAsset(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "airgate-core-linux-amd64")
	content := []byte("binary")
	if err := os.WriteFile(target, content, 0o600); err != nil {
		t.Fatalf("写入待校验文件失败: %v", err)
	}
	sum := sha256.Sum256(content)
	checksum := hex.EncodeToString(sum[:]) + "  airgate-core-linux-amd64\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(checksum))
	}))
	defer server.Close()

	service := NewService(ModeSystemd, nil)
	service.github = &githubClient{
		cached: &ReleaseInfo{
			Assets: []Asset{{Name: "airgate-core-linux-amd64.sha256", DownloadURL: server.URL}},
		},
		cacheExpire: time.Now().Add(time.Hour),
	}

	err := service.verifyChecksum(&Asset{Name: "airgate-core-linux-amd64"}, target)
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
}

func TestFailStoresFailedStatus(t *testing.T) {
	service := NewService(ModeSystemd, nil)
	service.fail("v2", "下载失败", errors.New("网络错误"))

	status := service.Status()
	if status.State != StateFailed || status.Target != "v2" || status.Message != "下载失败" || status.Error != "网络错误" {
		t.Fatalf("失败状态异常: %+v", status)
	}
}
