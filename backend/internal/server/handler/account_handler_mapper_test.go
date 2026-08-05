package handler

import (
	"reflect"
	"testing"

	appaccount "github.com/DouDOU-start/airgate-core/internal/app/account"
)

func TestToAccountExportItemPreservesRoutingBindings(t *testing.T) {
	account := appaccount.Account{
		Name:     "测试账号",
		Platform: "codex",
		GroupIDs: []int64{3, 7},
		Proxy:    &appaccount.ProxyRef{ID: 11},
	}

	item := toAccountExportItem(account)

	if !reflect.DeepEqual(item.GroupIDs, []int{3, 7}) {
		t.Fatalf("分组绑定未导出：%v", item.GroupIDs)
	}
	if item.ProxyID == nil || *item.ProxyID != 11 {
		t.Fatalf("代理绑定未导出：%v", item.ProxyID)
	}
}
