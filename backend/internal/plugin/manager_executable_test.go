package plugin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestFindPluginExecutablePathPrefersWindowsExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows executable naming only")
	}
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "demo")
	currentPath := filepath.Join(dir, "demo.exe")
	if err := os.WriteFile(legacyPath, []byte("legacy"), 0600); err != nil {
		t.Fatalf("write legacy executable: %v", err)
	}
	if err := os.WriteFile(currentPath, []byte("current"), 0600); err != nil {
		t.Fatalf("write current executable: %v", err)
	}

	got, ok := findPluginExecutablePath(dir, "demo")
	if !ok || got != currentPath {
		t.Fatalf("findPluginExecutablePath() = %q, %v, want %q, true", got, ok, currentPath)
	}
}

func TestFindPluginExecutablePathFallsBackToLegacyWindowsExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows executable naming only")
	}
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "demo")
	if err := os.WriteFile(legacyPath, []byte("legacy"), 0600); err != nil {
		t.Fatalf("write legacy executable: %v", err)
	}

	got, ok := findPluginExecutablePath(dir, "demo")
	if !ok || got != legacyPath {
		t.Fatalf("findPluginExecutablePath() = %q, %v, want %q, true", got, ok, legacyPath)
	}
}

func TestLoadAllAndReloadUseLegacyExtensionlessWindowsExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows executable naming only")
	}
	pluginDir := t.TempDir()
	binaryDir := filepath.Join(pluginDir, "legacy-hook")
	if err := os.MkdirAll(binaryDir, 0755); err != nil {
		t.Fatalf("create legacy plugin directory: %v", err)
	}
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatalf("read test binary: %v", err)
	}
	legacyPath := filepath.Join(binaryDir, "legacy-hook")
	if err := os.WriteFile(legacyPath, binary, 0755); err != nil {
		t.Fatalf("write legacy plugin binary: %v", err)
	}
	config := []byte("from_yaml: loaded\nnested:\n  enabled: true\n")
	if err := os.WriteFile(filepath.Join(binaryDir, "config.yaml"), config, 0600); err != nil {
		t.Fatalf("write legacy plugin config: %v", err)
	}

	t.Setenv(relayHookV2HelperEnv, "probe")
	manager := NewManager(pluginDir, "warn", "postgres://must-not-be-injected", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := manager.LoadAll(ctx); err != nil {
		t.Fatalf("LoadAll() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(binaryDir, "legacy-hook.exe")); err != nil {
		t.Fatalf("legacy plugin was not migrated to .exe: %v", err)
	}
	const canonicalName = "relay-hook-v2-helper"
	first := manager.GetInstance(canonicalName)
	if first == nil || first.RelayHookV2 == nil {
		t.Fatalf("legacy plugin not loaded: %#v", first)
	}
	defer manager.stopPlugin(canonicalName)

	if err := manager.ReloadInstance(ctx, canonicalName); err != nil {
		t.Fatalf("ReloadInstance() error = %v", err)
	}
	reloaded := manager.GetInstance(canonicalName)
	if reloaded == nil || reloaded.RelayHookV2 == nil {
		t.Fatalf("legacy plugin not reloaded: %#v", reloaded)
	}
	if reloaded == first || reloaded.Client == first.Client {
		t.Fatal("ReloadInstance() reused the previous runtime")
	}
}
