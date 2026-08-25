package i18n

import (
	"embed"
	"encoding/json"
	"log/slog"
)

//go:embed locales/*.json
var localeFS embed.FS

// LoadEmbedded 从二进制内嵌的 locales/*.json 加载翻译。
// 与 Load(dir) 不同的是，调用方不需要确保运行目录下存在 locales/ 目录 —— 这
// 是裸金属 install.sh / systemd 部署能正常工作的前提。
func LoadEmbedded() error {
	files := []string{"zh.json", "en.json"}
	for _, f := range files {
		data, err := localeFS.ReadFile("locales/" + f)
		if err != nil {
			slog.Warn("加载嵌入翻译文件失败", "file", f, "error", err)
			continue
		}
		var msgs map[string]string
		if err := json.Unmarshal(data, &msgs); err != nil {
			slog.Warn("解析嵌入翻译文件失败", "file", f, "error", err)
			continue
		}
		lang := f[:len(f)-5]
		mu.Lock()
		translations[lang] = msgs
		mu.Unlock()
		slog.Info("加载嵌入翻译", "lang", lang, "keys", len(msgs))
	}
	return nil
}
