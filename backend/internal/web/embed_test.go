package web

import (
	"io/fs"
	"strings"
	"testing"
)

func TestIndexHTMLReadsEmbeddedFile(t *testing.T) {
	data, err := IndexHTML()
	if err != nil {
		t.Skipf("跳过: 嵌入资源不可用 (需先构建前端): %v", err)
	}
	if !strings.Contains(string(data), "<!doctype html>") && !strings.Contains(string(data), "<!DOCTYPE html>") {
		t.Fatalf("index.html 内容不像 HTML: %q", string(data[:min(len(data), 32)]))
	}
}

func TestFSContainsIndexHTML(t *testing.T) {
	sub, err := FS()
	if err != nil {
		t.Skipf("跳过: 嵌入资源不可用 (需先构建前端): %v", err)
	}
	info, err := fs.Stat(sub, "index.html")
	if err != nil {
		t.Fatalf("嵌入文件系统缺少 index.html: %v", err)
	}
	if info.IsDir() {
		t.Fatal("index.html 不应是目录")
	}
}
