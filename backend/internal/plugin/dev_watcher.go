package plugin

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// dev_watcher.go：监听 dev 模式插件源码目录的 .go 文件改动，自动 ReloadDev。
//
// 实现选型：**mtime 轮询**而不是 fsnotify。
//
// 之所以不用 fsnotify：本项目主要在 WSL2 + /mnt/e（9p drvfs 挂载的 Windows 盘）
// 上开发，9p 文件系统**不会向 Linux inotify 投递事件**——fsnotify 可以正常注册
// 监听，但永远收不到回调。这是 WSL2 的已知限制，不是我们的 bug。
// 相同的原因，airgate-core 的 .air.toml 也开了 build.poll=true 才能让 air 在
// 同一份代码上工作。
//
// 轮询方案的代价是 1~2 秒的延迟和一点点 CPU；好处是在所有文件系统上都能工作
// （ext4/9p/NFS/SMB 都行），而且实现非常直接：扫描注册的 srcPath，比较 .go
// 文件的最大 mtime，发现增长就触发 ReloadInstance。
//
// 设计原则：
//   - 只在 dev 模式下启用（生产部署没有 srcPath）。
//   - 每个插件独立维护 lastMtime；任意 .go 文件 mtime 比上次记录大就 reload。
//   - 只关心 .go 文件（_test.go 排除）；前端 .ts/.tsx 走插件自己的 vite watch。
//   - reload 失败只 log warn，不 panic——dev 体验下不能因一次 build 失败让 watcher 死。
//
// 启动方式：Manager.LoadDev 成功后调一次 add(name, srcPath)，把 srcPath 加入
// 轮询集合。stopPlugin 时 remove。

type devWatcher struct {
	mgr      *Manager
	interval time.Duration

	mu      sync.Mutex
	plugins map[string]*devWatchEntry // canonicalName → entry

	stop chan struct{}
}

type devWatchEntry struct {
	srcPath   string
	lastMtime time.Time
	reloading bool // 防止 reload 期间 polling 又触发一次
}

const devWatcherInterval = 1500 * time.Millisecond

// newDevWatcher 创建一个新的 watcher，并启动后台 polling goroutine。
func newDevWatcher(mgr *Manager) *devWatcher {
	dw := &devWatcher{
		mgr:      mgr,
		interval: devWatcherInterval,
		plugins:  make(map[string]*devWatchEntry),
		stop:     make(chan struct{}),
	}
	go dw.loop()
	return dw
}

// add 把一个 dev 插件的 srcPath 加入轮询集合。
//
// 第一次扫描会把当前 max mtime 记下作为基线；这意味着 add 之后的下一次轮询
// 不会立刻 reload（防止 LoadDev 刚结束就被自己触发）。
func (dw *devWatcher) add(name, srcPath string) {
	baseline, _ := scanMaxGoMtime(srcPath)

	dw.mu.Lock()
	dw.plugins[name] = &devWatchEntry{srcPath: srcPath, lastMtime: baseline}
	dw.mu.Unlock()

	slog.Info("dev 插件源码 watch 已就绪 (mtime polling)",
		"plugin", name, "src", srcPath, "interval", dw.interval, "baseline", baseline)
}

// remove 在 stopPlugin 时调用，把插件从轮询集合移除。
func (dw *devWatcher) remove(name string) {
	dw.mu.Lock()
	defer dw.mu.Unlock()
	delete(dw.plugins, name)
}

// loop 每 interval 扫描一次所有注册插件，发现 mtime 增长就触发 reload。
func (dw *devWatcher) loop() {
	t := time.NewTicker(dw.interval)
	defer t.Stop()
	for {
		select {
		case <-dw.stop:
			return
		case <-t.C:
			dw.tick()
		}
	}
}

func (dw *devWatcher) tick() {
	// 拷贝一份快照，避免长时间持锁扫描磁盘
	dw.mu.Lock()
	snapshot := make(map[string]*devWatchEntry, len(dw.plugins))
	for k, v := range dw.plugins {
		if v.reloading {
			continue
		}
		snapshot[k] = v
	}
	dw.mu.Unlock()

	for name, entry := range snapshot {
		latest, ok := scanMaxGoMtime(entry.srcPath)
		if !ok {
			continue
		}
		if !latest.After(entry.lastMtime) {
			continue
		}

		// 找到变更：更新 baseline 并触发 reload。
		dw.mu.Lock()
		current, exists := dw.plugins[name]
		if !exists || current.reloading {
			dw.mu.Unlock()
			continue
		}
		current.lastMtime = latest
		current.reloading = true
		dw.mu.Unlock()

		go dw.doReload(name, latest)
	}
}

func (dw *devWatcher) doReload(name string, trigger time.Time) {
	defer func() {
		dw.mu.Lock()
		if e, ok := dw.plugins[name]; ok {
			e.reloading = false
			// reload 完之后再次扫一次，把 reload 期间产生的新 mtime 也吞掉，
			// 否则用户在 reload 还在跑的时候继续编辑，下次 tick 又会立刻触发
			// 一次（其实也无害，只是多一次 build）。
			if latest, ok := scanMaxGoMtime(e.srcPath); ok && latest.After(e.lastMtime) {
				e.lastMtime = latest
			}
		}
		dw.mu.Unlock()
	}()

	slog.Info("dev 插件源码变更，触发热重载", "plugin", name, "trigger_mtime", trigger)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := dw.mgr.ReloadInstance(ctx, name); err != nil {
		slog.Warn("dev 插件热重载失败", "plugin", name, "error", err)
		return
	}
	slog.Info("dev 插件热重载完成", "plugin", name)
}

// scanMaxGoMtime 递归扫描 root 下所有 .go 文件（排除 _test.go 与噪声目录），
// 返回最大 mtime。如果一个 .go 都没有，返回 (zero, false)。
//
// 排除规则与原 fsnotify 版本一致：vendor / node_modules / .git / tmp / dist /
// webdist / 隐藏目录。
func scanMaxGoMtime(root string) (time.Time, bool) {
	var max time.Time
	found := false
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == "vendor" || base == "node_modules" || base == "tmp" ||
				base == "dist" || base == "webdist" || base == ".git" {
				return filepath.SkipDir
			}
			if strings.HasPrefix(base, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		name := info.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		if mt := info.ModTime(); mt.After(max) {
			max = mt
			found = true
		}
		return nil
	})
	return max, found
}
