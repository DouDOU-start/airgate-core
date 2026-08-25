// Package version 暴露 core 的版本号。
//
// 默认值 "dev" 仅用于本地 go build / go run。正式发版由 Makefile（或 release
// workflow）通过 -ldflags "-X github.com/DouDOU-start/airgate-core/internal/version.Version=$tag"
// 注入 git tag。
package version

var Version = "dev"
