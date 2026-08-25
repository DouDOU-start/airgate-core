// Package upgrade 实现 core 自更新能力。
//
// 设计要点：
//   - 不同部署模式有不同的"重启"语义。systemd 走二进制原子替换 + 进程退出，
//     依赖 Restart=always；Docker 走外部 compose pull 指令；其余环境（go run）
//     完全禁用。
//   - 升级前在子进程里跑 `<new> --version` 做 smoke test，避免下载到错架构的二
//     进制后还硬替换。smoke test 失败 = 自动放弃，原 binary 不动。
//   - 状态机由 Service 串行驱动；Redis 锁防并发；GitHub 查询结果内存缓存 10 分钟。
package upgrade

import (
	"sync"
	"time"
)

// Mode 升级模式（部署形态）。
type Mode string

const (
	ModeSystemd Mode = "systemd"
	ModeDocker  Mode = "docker"
	ModeNoop    Mode = "noop"
)

// State 升级状态机的离散状态。
type State string

const (
	StateIdle        State = "idle"
	StateChecking    State = "checking"
	StateDownloading State = "downloading"
	StateVerifying   State = "verifying"
	StateSwapping    State = "swapping"
	StateRestarting  State = "restarting"
	StateFailed      State = "failed"
	StateSuccess     State = "success"
)

// ReleaseInfo GitHub release 关键信息。
type ReleaseInfo struct {
	TagName   string    `json:"tag_name"`
	Name      string    `json:"name"`
	Body      string    `json:"body"`
	HTMLURL   string    `json:"html_url"`
	Assets    []Asset   `json:"assets"`
	FetchedAt time.Time `json:"fetched_at"`
}

// Asset GitHub release 单个二进制资产。
type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
	Size        int64  `json:"size"`
}

// Info 给前端的升级总览数据。
type Info struct {
	Mode         Mode    `json:"mode"`
	Current      string  `json:"current"`
	Latest       string  `json:"latest,omitempty"`
	HasUpdate    bool    `json:"has_update"`
	ReleaseURL   string  `json:"release_url,omitempty"`
	ReleaseNotes string  `json:"release_notes,omitempty"`
	Instructions string  `json:"instructions,omitempty"` // Docker 模式下的升级命令
	CanUpgrade   bool    `json:"can_upgrade"`            // mode != noop && hasUpdate
	BinaryPath   string  `json:"binary_path,omitempty"`  // systemd 模式下展示
	CheckedAt    *string `json:"checked_at,omitempty"`
}

// Status 状态查询响应。
type Status struct {
	State    State   `json:"state"`
	Progress float64 `json:"progress"` // 0~1，仅 downloading 阶段有意义
	Message  string  `json:"message,omitempty"`
	Error    string  `json:"error,omitempty"`
	Target   string  `json:"target,omitempty"` // 目标版本号
}

// RunRequest /upgrade/run 请求体。
type RunRequest struct {
	ConfirmDBBackup bool `json:"confirm_db_backup"`
}

// statusBox 内部线程安全状态。
type statusBox struct {
	mu     sync.RWMutex
	status Status
}

func (b *statusBox) load() Status {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.status
}

func (b *statusBox) store(s Status) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status = s
}

func (b *statusBox) update(fn func(*Status)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	fn(&b.status)
}
