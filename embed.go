// Package gocrawler 在 repo 根持有 migrations 的 embed.FS,
// 因為 go:embed 只能引用同 package 目錄下的檔案。
package gocrawler

import "embed"

//go:embed migrations/*.sql
var MigrationsFS embed.FS
