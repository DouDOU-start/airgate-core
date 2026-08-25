package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"

	"github.com/DouDOU-start/airgate-core/internal/version"
)

// redisLockKey 全局升级互斥锁。
const redisLockKey = "airgate:upgrade:lock"
const redisLockTTL = 10 * time.Minute

// Service 升级服务，对外暴露 Info / Status / Run 三个能力。
type Service struct {
	mode   Mode
	github *githubClient
	rdb    *redis.Client
	box    *statusBox
}

// NewService 构造升级服务。mode 由 DetectMode() 在外层探测后注入，便于测试覆盖。
func NewService(mode Mode, rdb *redis.Client) *Service {
	return &Service{
		mode:   mode,
		github: newGithubClient(),
		rdb:    rdb,
		box:    &statusBox{status: Status{State: StateIdle}},
	}
}

// Mode 暴露当前部署模式。
func (s *Service) Mode() Mode { return s.mode }

// Info 拼装升级总览。会触发 GitHub 查询（带缓存），所以可能较慢。
func (s *Service) Info(ctx context.Context) (*Info, error) {
	current := version.Version
	info := &Info{
		Mode:    s.mode,
		Current: current,
	}
	slog.Info("upgrade_check_start", "current_version", current, "mode", string(s.mode))

	if s.mode == ModeSystemd {
		if exe, err := os.Executable(); err == nil {
			info.BinaryPath = exe
		}
	}

	rel, err := s.github.LatestRelease(ctx)
	if err != nil {
		// GitHub 失败不算硬错误：前端仍能展示当前版本，按钮置灰即可。
		slog.Warn("upgrade_check_failed", sdk.LogFieldError, err)
		return info, nil
	}

	info.Latest = rel.TagName
	info.ReleaseURL = rel.HTMLURL
	info.ReleaseNotes = rel.Body
	if t := rel.FetchedAt.Format(time.RFC3339); t != "" {
		info.CheckedAt = &t
	}
	info.HasUpdate = isNewer(rel.TagName, current)
	if info.HasUpdate {
		slog.Info("upgrade_available", "latest", rel.TagName, "current", current)
	}

	switch s.mode {
	case ModeDocker:
		info.Instructions = "docker compose pull && docker compose up -d"
		info.CanUpgrade = false
	case ModeSystemd:
		info.CanUpgrade = info.HasUpdate
	default:
		info.CanUpgrade = false
	}
	return info, nil
}

// Status 返回状态机当前快照。
func (s *Service) Status() Status { return s.box.load() }

// Run 触发升级流程（仅 systemd 模式有效）。
//
// 行为：
//  1. 校验 confirm_db_backup
//  2. 抢 Redis 锁（防并发）
//  3. 异步执行下载/校验/smoke test/swap，最后 os.Exit(0) 让 systemd 拉起
//  4. 任何步骤失败 → 状态置 failed + slog.Error 告警，原 binary 不动
func (s *Service) Run(ctx context.Context, req RunRequest) error {
	if s.mode != ModeSystemd {
		return fmt.Errorf("当前部署模式 %s 不支持一键升级", s.mode)
	}
	if !req.ConfirmDBBackup {
		return errors.New("请先确认已备份数据库（confirm_db_backup=true）")
	}

	// 状态预检：避免在已有任务进行中重复触发
	cur := s.box.load()
	if cur.State != StateIdle && cur.State != StateFailed && cur.State != StateSuccess {
		return fmt.Errorf("升级任务正在进行中（state=%s）", cur.State)
	}

	// Redis 锁（兜底，集群部署场景下也能防住）
	if s.rdb != nil {
		ok, err := s.rdb.SetNX(ctx, redisLockKey, "1", redisLockTTL).Result()
		if err != nil {
			return fmt.Errorf("获取升级锁失败: %w", err)
		}
		if !ok {
			return errors.New("已有升级任务正在进行")
		}
	}

	// 拉一次 release 元数据（同步，便于把 target 立即写进状态）
	rel, err := s.github.LatestRelease(ctx)
	if err != nil {
		s.releaseLock(context.Background())
		return fmt.Errorf("拉取最新 release 失败: %w", err)
	}
	asset, err := pickAsset(rel.Assets, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		s.releaseLock(context.Background())
		return err
	}

	s.box.store(Status{
		State:   StateDownloading,
		Target:  rel.TagName,
		Message: "开始下载新版本",
	})

	// 异步执行升级，handler 立即返回
	go s.runAsync(rel.TagName, asset)
	return nil
}

func (s *Service) releaseLock(ctx context.Context) {
	if s.rdb == nil {
		return
	}
	_ = s.rdb.Del(ctx, redisLockKey).Err()
}

// runAsync 真正执行升级。所有错误都会写入 status + slog.Error。
func (s *Service) runAsync(target string, asset *Asset) {
	defer s.releaseLock(context.Background())

	exe, err := os.Executable()
	if err != nil {
		s.fail(target, "无法定位当前 binary 路径", err)
		return
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		s.fail(target, "解析 binary 软链失败", err)
		return
	}

	newPath := exe + ".new"
	bakPath := exe + ".bak"

	// === 下载 ===
	if err := s.download(asset, newPath); err != nil {
		s.fail(target, "下载新版本失败", err)
		_ = os.Remove(newPath)
		return
	}

	// === sha256 校验 ===
	s.box.update(func(st *Status) { st.State = StateVerifying; st.Message = "校验 SHA256" })
	if err := s.verifyChecksum(asset, newPath); err != nil {
		s.fail(target, "SHA256 校验失败", err)
		_ = os.Remove(newPath)
		return
	}

	// === 标记可执行 ===
	if err := os.Chmod(newPath, 0o755); err != nil {
		s.fail(target, "设置可执行位失败", err)
		_ = os.Remove(newPath)
		return
	}

	// === smoke test：直接 exec 新 binary 跑 --version ===
	s.box.update(func(st *Status) { st.Message = "执行 smoke test" })
	if err := smokeTest(newPath); err != nil {
		s.fail(target, "新版本 smoke test 失败，已放弃替换", err)
		_ = os.Remove(newPath)
		return
	}

	// === 原子替换 ===
	s.box.update(func(st *Status) { st.State = StateSwapping; st.Message = "替换 binary" })
	// 备份当前 binary
	if err := copyFile(exe, bakPath); err != nil {
		s.fail(target, "备份当前 binary 失败", err)
		_ = os.Remove(newPath)
		return
	}
	if err := os.Rename(newPath, exe); err != nil {
		// 回滚：rename 失败时 .bak 已经存在但 exe 没动，理论上无害；保险起见删 .new
		_ = os.Remove(newPath)
		s.fail(target, "原子替换 binary 失败，原版本未受影响", err)
		return
	}

	slog.Info("upgrade_migration_applied",
		"from", version.Version,
		"to", target,
		"backup", bakPath,
	)

	s.box.store(Status{
		State:   StateRestarting,
		Target:  target,
		Message: "binary 已替换，正在退出以触发 systemd 重启",
	})

	// 给 HTTP 响应一点时间送出，再退出
	go func() {
		time.Sleep(800 * time.Millisecond)
		slog.Warn("upgrade_self_exit",
			"target", target,
			"rollback_hint", "如反复重启，运行 `mv "+bakPath+" "+exe+"` 回滚",
		)
		os.Exit(0)
	}()
}

func (s *Service) fail(target, msg string, err error) {
	slog.Error("upgrade_migration_failed",
		"from", version.Version,
		"to", target,
		"stage", msg,
		sdk.LogFieldError, err)
	s.box.store(Status{
		State:   StateFailed,
		Target:  target,
		Message: msg,
		Error:   err.Error(),
	})
}

// download 边写边算 progress。
func (s *Service) download(asset *Asset, dst string) error {
	resp, err := http.Get(asset.DownloadURL)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP 状态码 %d", resp.StatusCode)
	}

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	total := asset.Size
	if total <= 0 {
		total = resp.ContentLength
	}

	buf := make([]byte, 64*1024)
	var written int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			written += int64(n)
			if total > 0 {
				p := float64(written) / float64(total)
				s.box.update(func(st *Status) {
					st.State = StateDownloading
					st.Progress = p
					st.Message = fmt.Sprintf("下载中 %d/%d", written, total)
				})
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	return nil
}

// verifyChecksum 优先在同一个 release 里查找 <asset>.sha256 资产；如果不存在，
// 退化为校验文件长度（避免完全没有保护）。
func (s *Service) verifyChecksum(asset *Asset, path string) error {
	rel, err := s.github.LatestRelease(context.Background())
	if err != nil {
		return err
	}
	var checksumAsset *Asset
	for i := range rel.Assets {
		if rel.Assets[i].Name == asset.Name+".sha256" {
			checksumAsset = &rel.Assets[i]
			break
		}
	}
	if checksumAsset == nil {
		// release.yml 已经会上传 .sha256，缺失视为异常
		return errors.New("release 缺少 .sha256 校验文件")
	}

	resp, err := http.Get(checksumAsset.DownloadURL)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	expected := strings.Fields(strings.TrimSpace(string(body)))
	if len(expected) == 0 {
		return errors.New("无法解析 .sha256 文件内容")
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(actual, expected[0]) {
		return fmt.Errorf("sha256 不匹配：expected=%s actual=%s", expected[0], actual)
	}
	return nil
}

// smokeTest 子进程执行 `<path> --version`，要求 5 秒内退出 0 且输出含 "airgate-core"。
func smokeTest(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return err
	}
	if !strings.Contains(string(out), "airgate-core") {
		return fmt.Errorf("--version 输出异常: %q", string(out))
	}
	return nil
}

// pickAsset 按 GOOS/GOARCH 在资产列表里找匹配的 binary 资产。
// release.yml 命名规则：airgate-core-<goos>-<goarch>。
func pickAsset(assets []Asset, goos, goarch string) (*Asset, error) {
	want := fmt.Sprintf("airgate-core-%s-%s", goos, goarch)
	for i := range assets {
		if assets[i].Name == want {
			return &assets[i], nil
		}
	}
	return nil, fmt.Errorf("release 中未找到匹配资产 %s", want)
}

// copyFile 用于备份当前 binary。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
}

// isNewer 判断 latest 是否比 current 新。
//
// 简化策略：
//   - current 为 "dev"、空、或包含 "-dirty" → 总是认为有更新
//   - 否则做 v 前缀去除后字符串比较；release tag 走 semver 也接受
//
// 这里不引第三方 semver 库，少一个依赖；prerelease 比较留给 GitHub 端按时间排序。
func isNewer(latest, current string) bool {
	if latest == "" {
		return false
	}
	if current == "" || current == "dev" || strings.Contains(current, "-dirty") {
		return true
	}
	return strings.TrimPrefix(latest, "v") != strings.TrimPrefix(current, "v")
}
