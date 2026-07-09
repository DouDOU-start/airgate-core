// Package priceseed 在 core 启动时把默认模型价目表种子导入 modelprice 表。
//
// 语义为 insert-if-absent：只导入价目表里尚不存在的 model，已存在的（含管理员手动
// 改过的）绝不覆盖。空表 / 解析失败 / 落库失败一律只 Warn，不阻塞启动。
//
// 种子来源双通道（优先级从高到低）：
//  1. 环境变量 MODEL_PRICES_SEED 指定的外部文件路径；
//  2. <config.yaml 同目录>/model-prices.seed.yaml；
//  3. 二进制内嵌的默认种子（本包 embed 的 model-prices.seed.yaml）。
//
// 仓库内 backend/data/model-prices.seed.yaml 是同内容的「可编辑源 / 部署样例」，
// 本包 embed 副本与其内容一致（见 priceseed_test.go 的一致性断言）。
package priceseed

import (
	"context"
	_ "embed"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"

	appmodelprice "github.com/DouDOU-start/airgate-core/internal/app/modelprice"
)

//go:embed model-prices.seed.yaml
var embeddedSeed []byte

// externalFileName 是外部可编辑种子文件的约定文件名（放在 config.yaml 同目录）。
const externalFileName = "model-prices.seed.yaml"

// envSeedPath 是覆盖种子文件路径的环境变量名。
const envSeedPath = "MODEL_PRICES_SEED"

// PriceStore 是种子导入所需的 modelprice 仓储子集。
// *store.ModelPriceStore 天然满足此接口。
type PriceStore interface {
	ListAll(ctx context.Context) ([]appmodelprice.ModelPrice, error)
	Create(ctx context.Context, input appmodelprice.CreateInput) (appmodelprice.ModelPrice, error)
	// EnsureTag 按名称 find-or-create 标签，返回标签 ID。
	EnsureTag(ctx context.Context, name string) (int, error)
}

// Load 解析种子文件并对每个 model 执行 insert-if-absent 导入。
// 任何失败都只记 Warn 日志，不返回错误、不阻塞启动。
// configPath 为主配置文件路径，用于推导外部种子文件的默认位置（可为空）。
func Load(ctx context.Context, store PriceStore, configPath string) {
	data, source := resolveSource(configPath)

	items, err := Parse(data)
	if err != nil {
		slog.Warn("price_seed_parse_failed", "source", source, logx.LogFieldError, err)
		return
	}
	if len(items) == 0 {
		slog.Warn("price_seed_empty", "source", source)
		return
	}

	inserted, skipped, err := insertMissing(ctx, store, items)
	if err != nil {
		// insertMissing 内部对单条失败已降级为 continue；此处仅 ListAll 失败会触发。
		slog.Warn("price_seed_failed", "source", source, logx.LogFieldError, err)
		return
	}

	slog.Info("price_seed_done", "inserted", inserted, "skipped_existing", skipped, "source", source)
}

// resolveSource 按优先级返回种子字节与来源标签。
func resolveSource(configPath string) ([]byte, string) {
	// 1) 环境变量指定的外部路径
	if p := os.Getenv(envSeedPath); p != "" {
		if data, err := os.ReadFile(p); err == nil {
			return data, p
		} else {
			slog.Warn("price_seed_external_read_failed", "path", p, logx.LogFieldError, err)
		}
	}

	// 2) config.yaml 同目录下的约定文件
	if configPath != "" {
		p := filepath.Join(filepath.Dir(configPath), externalFileName)
		if data, err := os.ReadFile(p); err == nil {
			return data, p
		}
	}

	// 3) 内嵌默认种子
	return embeddedSeed, "embed"
}

// insertMissing 只对价目表中尚不存在的 model 调用 Create，绝不更新已存在条目。
// 返回新建条数与跳过（已存在）条数。ListAll 失败返回 error；单条 Create 失败降级为 Warn+continue；
// 标签 EnsureTag 失败降级为「无标签插入」（不因标签阻塞价格种子）。
func insertMissing(ctx context.Context, store PriceStore, items []SeedItem) (inserted, skipped int, err error) {
	existing, err := store.ListAll(ctx)
	if err != nil {
		return 0, 0, err
	}

	present := make(map[string]struct{}, len(existing))
	for _, e := range existing {
		present[e.Model] = struct{}{}
	}

	tagIDs := map[string]int{} // 标签名 → ID 缓存（同名只 EnsureTag 一次）
	for _, item := range items {
		if _, ok := present[item.Model]; ok {
			skipped++
			continue
		}
		input := item.CreateInput
		if item.TagName != "" {
			id, ok := tagIDs[item.TagName]
			if !ok {
				var terr error
				if id, terr = store.EnsureTag(ctx, item.TagName); terr != nil {
					slog.Warn("price_seed_tag_failed", "model", item.Model, "tag", item.TagName, logx.LogFieldError, terr)
					id = 0
				}
				tagIDs[item.TagName] = id
			}
			if id > 0 {
				tagID := id
				input.TagID = &tagID
			}
		}
		if _, cerr := store.Create(ctx, input); cerr != nil {
			slog.Warn("price_seed_insert_failed", "model", item.Model, logx.LogFieldError, cerr)
			continue
		}
		// 记入 present，防止种子内重复 model 触发二次 Create。
		present[item.Model] = struct{}{}
		inserted++
	}
	return inserted, skipped, nil
}
