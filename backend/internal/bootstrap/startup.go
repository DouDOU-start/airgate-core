package bootstrap

import (
	"context"
	"log/slog"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/apikey"
	"github.com/DouDOU-start/airgate-core/internal/auth"
)

// RunStartupTasks 运行启动阶段的整理任务。
func RunStartupTasks(db *ent.Client, apiKeySecret string) {
	backfillKeyHints(db, apiKeySecret)
}

// backfillKeyHints 为缺少或格式过旧的 key_hint 回填 sk-xxxx...xxxx。
func backfillKeyHints(db *ent.Client, secret string) {
	ctx := context.Background()
	keys, err := db.APIKey.Query().
		Where(apikey.Or(
			apikey.KeyHint(""),
			apikey.KeyHintHasPrefix("sk-..."),
		)).
		All(ctx)
	if err != nil {
		slog.Warn("查询待回填 API Key 失败", "error", err)
		return
	}
	if len(keys) == 0 {
		return
	}

	slog.Info("回填 API Key hint", "count", len(keys))
	for _, item := range keys {
		if item.KeyEncrypted == "" {
			continue
		}
		plain, err := auth.DecryptAPIKey(item.KeyEncrypted, secret)
		if err != nil {
			slog.Warn("解密 API Key 失败，跳过", "id", item.ID, "error", err)
			continue
		}
		hint := plain[:7] + "..." + plain[len(plain)-4:]
		if err := db.APIKey.UpdateOneID(item.ID).SetKeyHint(hint).Exec(ctx); err != nil {
			slog.Warn("回填 key_hint 失败", "id", item.ID, "error", err)
		}
	}
	slog.Info("API Key hint 回填完成")
}
