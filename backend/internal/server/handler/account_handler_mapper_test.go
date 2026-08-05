package handler

import (
	"testing"

	appaccount "github.com/DouDOU-start/airgate-core/internal/app/account"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

func TestToAccountExportItemOmitsLocalBindings(t *testing.T) {
	account := appaccount.Account{
		Name:     "测试账号",
		Platform: "codex",
		GroupIDs: []int64{3, 7},
		Proxy:    &appaccount.ProxyRef{ID: 11},
	}

	item := toAccountExportItem(account)

	if len(item.GroupIDs) != 0 {
		t.Fatalf("导出不应包含分组绑定：%v", item.GroupIDs)
	}
	if item.ProxyID != nil {
		t.Fatalf("导出不应包含代理绑定：%v", *item.ProxyID)
	}
}

func TestToAccountImportInputIgnoresLocalBindings(t *testing.T) {
	proxyID := int64(11)
	item := dto.AccountExportItem{
		Name:           "测试账号",
		Platform:       "codex",
		Type:           "oauth",
		Credentials:    map[string]string{"access_token": "token"},
		Priority:       2,
		Weight:         3,
		MaxConcurrency: 4,
		RateMultiplier: 1.5,
		GroupIDs:       []int{3, 7},
		ProxyID:        &proxyID,
		Extra:          map[string]any{"source": "export"},
	}

	input := toAccountImportInput(item)

	if len(input.GroupIDs) != 0 {
		t.Fatalf("导入不应绑定旧分组：%v", input.GroupIDs)
	}
	if input.ProxyID != nil {
		t.Fatalf("导入不应绑定旧代理：%v", *input.ProxyID)
	}
	if input.Name != item.Name || input.Platform != item.Platform || input.Type != item.Type {
		t.Fatalf("导入账号基础字段未正确保留：%+v", input)
	}
}
