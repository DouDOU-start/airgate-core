package handler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	appaccount "github.com/DouDOU-start/airgate-core/internal/app/account"
)

func TestToAccountExportItemOmitsEnvironmentScopedIDs(t *testing.T) {
	item := toAccountExportItem(appaccount.Account{
		Name:           "demo",
		Platform:       "openai",
		Type:           "apikey",
		Credentials:    map[string]string{"api_key": "secret"},
		Priority:       2,
		MaxConcurrency: 4,
		RateMultiplier: 1.5,
		GroupIDs:       []int64{2, 1},
		Proxy: &appaccount.Proxy{
			ID: 7,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	if len(item.GroupIDs) != 0 {
		t.Fatalf("expected export item group IDs to be empty, got %v", item.GroupIDs)
	}
	if item.ProxyID != nil {
		t.Fatalf("expected export item proxy ID to be nil, got %v", *item.ProxyID)
	}

	payload, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal export item: %v", err)
	}
	jsonText := string(payload)
	if strings.Contains(jsonText, "group_ids") {
		t.Fatalf("expected export JSON to omit group_ids, got %s", jsonText)
	}
	if strings.Contains(jsonText, "proxy_id") {
		t.Fatalf("expected export JSON to omit proxy_id, got %s", jsonText)
	}
}
