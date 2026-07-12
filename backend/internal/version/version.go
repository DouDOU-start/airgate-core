// Package version 暴露 core 的版本号。
//
// 默认写死为发布版本号；本地 go build / go run 直接显示该值。仍可由 Makefile
// （或 release workflow）通过 -ldflags "-X github.com/DouDOU-start/airgate-core/internal/version.Version=$tag"
// 在构建期覆盖。
package version

var Version = "v1.0.0"
