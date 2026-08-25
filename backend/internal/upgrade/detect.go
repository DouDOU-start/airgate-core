package upgrade

import (
	"os"
	"runtime"
	"strings"
)

// DetectMode 探测当前部署形态。
//
// 顺序：Docker → systemd → noop。Docker 优先是因为容器内的 PPID 也常常是 1，
// 但 /.dockerenv 文件几乎只在容器里出现，可以稳定区分。
func DetectMode() Mode {
	if isDocker() {
		return ModeDocker
	}
	if isSystemd() {
		return ModeSystemd
	}
	return ModeNoop
}

// isDocker 通过 /.dockerenv 标记或 cgroup 内容判断。
func isDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	// 兜底：cgroup v1 包含 docker，cgroup v2 也常见 0::/docker/...
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		s := string(data)
		if strings.Contains(s, "docker") || strings.Contains(s, "containerd") {
			return true
		}
	}
	return false
}

// isSystemd 通过 PPID 是否为 1 + binary 路径可写判断。
//
// 注意：仅 Linux 下的 systemd 才有意义。darwin 上 Restart=always 等价物是 launchd，
// 但 launchd 行为差异较大，本期不支持，统一回退到 noop。
func isSystemd() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if os.Getppid() != 1 {
		return false
	}
	binary, err := os.Executable()
	if err != nil {
		return false
	}
	return isWritable(binary)
}

// isWritable 检查是否对指定文件具有写权限（沿父目录尝试创建临时文件作为兜底）。
func isWritable(path string) bool {
	// 尝试 O_WRONLY 打开本身（不会真的写入）
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err == nil {
		_ = f.Close()
		return true
	}
	// 兜底：检查父目录是否可写（rename 需要的是父目录写权限）
	dir := path[:strings.LastIndex(path, "/")+1]
	if dir == "" {
		return false
	}
	tmp, err := os.CreateTemp(dir, ".airgate-write-test-*")
	if err != nil {
		return false
	}
	name := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(name)
	return true
}
