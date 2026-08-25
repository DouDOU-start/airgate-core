// Package web 用 //go:embed 把构建后的前端 SPA 嵌入二进制。
//
// 构建流程依赖（Makefile / Dockerfile / release workflow 都遵循）：
//  1. cd web && pnpm build           生成 web/dist
//  2. rm -rf backend/internal/web/webdist
//  3. cp -r web/dist backend/internal/web/webdist
//  4. cd backend && go build ./cmd/server
//
// 这样得到的二进制完全自包含，不再依赖磁盘上的 web/dist 目录，
// install.sh / systemd 部署不需要单独发布静态资源。
//
// 仓库里 webdist/ 目录只放一个 .gitkeep —— go:embed 要求目标目录非空，
// 但我们不希望把构建产物提交进 git。CI / Makefile 在 build 前总会刷新这个目录。
package web

import (
	"embed"
	"errors"
	"io/fs"
)

//go:embed all:webdist
var distFS embed.FS

// FS 返回根目录指向 webdist/ 的子文件系统。
// 调用方可以直接 http.FS(web.FS()) 暴露给 gin / net/http。
func FS() (fs.FS, error) {
	sub, err := fs.Sub(distFS, "webdist")
	if err != nil {
		return nil, err
	}
	// 探测一下 index.html 是否存在；不存在意味着构建时漏了 cp 步骤，
	// 直接快速失败比让用户在浏览器里看 404 友好得多。
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, errors.New("embedded web/dist is empty: rebuild backend after running `make build-frontend` (or copy web/dist → backend/internal/web/webdist before go build)")
	}
	return sub, nil
}

// IndexHTML 返回嵌入的 index.html 内容。
// gin 的 c.Data() 需要原始字节，c.FileFromFS() 与 http.FS 配合也行。
func IndexHTML() ([]byte, error) {
	return distFS.ReadFile("webdist/index.html")
}
