package store

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	appchannel "github.com/DouDOU-start/airgate-core/internal/app/channel"
)

func createTestChannel(t *testing.T, db *ent.Client, name string) *ent.Channel {
	t.Helper()
	ch, err := db.Channel.Create().
		SetName(name).
		SetBaseURL("https://upstream.example.com").
		Save(context.Background())
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return ch
}

// createTestKey 在渠道下建一把 key，返回其 ID。
func createTestKey(t *testing.T, db *ent.Client, channelID int) int {
	t.Helper()
	k, err := db.ChannelKey.Create().
		SetChannelID(channelID).
		SetType("openai_compatible").
		SetAPIKey("cipher").
		SetModels([]string{"gpt-5"}).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create channel key: %v", err)
	}
	return k.ID
}

func TestChannelStoreGetChannelMoneyStats(t *testing.T) {
	db := enttestOpen(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()

	ctx := context.Background()
	chA := createTestChannel(t, db, "channel-a")
	chB := createTestChannel(t, db, "channel-b")
	keyA := createTestKey(t, db, chA.ID)
	keyB := createTestKey(t, db, chB.ID)
	keyIdle := createTestKey(t, db, chB.ID)

	now := time.Now()
	todayStart := now.Add(-time.Hour)
	yesterday := now.Add(-2 * time.Hour)

	// keyA：今日两条，成本 = 10×1.5 + 4×2 = 23，收益 = 8 + 3 = 11。
	// keyB：今日一条（成本 5×0.5 = 2.5，收益 6）+ 今日之前一条（成本 8×1 = 8，收益 2）。
	fixtures := []struct {
		keyID      int
		totalCost  float64
		accountRM  float64
		actualCost float64
		createdAt  time.Time
	}{
		{keyA, 10, 1.5, 8, now},
		{keyA, 4, 2, 3, now},
		{keyB, 5, 0.5, 6, now},
		{keyB, 8, 1, 2, yesterday},
	}
	for _, item := range fixtures {
		if _, err := db.UsageLog.Create().
			SetModel("gpt-5").
			SetChannelKeyID(item.keyID).
			SetTotalCost(item.totalCost).
			SetAccountRateMultiplier(item.accountRM).
			SetActualCost(item.actualCost).
			SetCreatedAt(item.createdAt).
			Save(ctx); err != nil {
			t.Fatalf("create usage log: %v", err)
		}
	}

	store := NewChannelStore(db)
	stats, err := store.GetChannelKeyMoneyStats(ctx, []int{keyA, keyB, keyIdle}, todayStart)
	if err != nil {
		t.Fatalf("GetChannelKeyMoneyStats returned error: %v", err)
	}

	assertMoney := func(name string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-9 {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
	assertMoney("keyA.Cost", stats[keyA].Cost, 23)
	assertMoney("keyA.Revenue", stats[keyA].Revenue, 11)
	assertMoney("keyA.TodayCost", stats[keyA].TodayCost, 23)
	assertMoney("keyA.TodayRevenue", stats[keyA].TodayRevenue, 11)
	assertMoney("keyB.Cost", stats[keyB].Cost, 10.5)
	assertMoney("keyB.Revenue", stats[keyB].Revenue, 8)
	assertMoney("keyB.TodayCost", stats[keyB].TodayCost, 2.5)
	assertMoney("keyB.TodayRevenue", stats[keyB].TodayRevenue, 6)

	// 无用量 key 不出现在聚合结果里，零值由调用方兜底。
	if _, ok := stats[keyIdle]; ok {
		t.Fatalf("idle key should not appear in stats")
	}

	// 空入参：返回空表不查库。
	empty, err := store.GetChannelKeyMoneyStats(ctx, nil, todayStart)
	if err != nil {
		t.Fatalf("GetChannelKeyMoneyStats(nil) returned error: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("GetChannelKeyMoneyStats(nil) = %v, want empty", empty)
	}
}

// TestChannelStoreCreateAndManageKeys 渠道建后独立增/改 key，每把绑定各自类型/模型。
func TestChannelStoreCreateAndManageKeys(t *testing.T) {
	db := enttestOpen(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()

	ctx := context.Background()
	store := NewChannelStore(db)

	created, err := store.Create(ctx, appchannel.CreateInput{
		Name:    "reseller",
		BaseURL: "https://custom.example.com",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := store.CreateKey(ctx, created.ID, appchannel.KeyInput{Name: "claude", Type: "anthropic", APIKey: "cipher-a", Models: []string{"claude-x"}}); err != nil {
		t.Fatalf("CreateKey(claude) error: %v", err)
	}
	if _, err := store.CreateKey(ctx, created.ID, appchannel.KeyInput{Name: "gemini", Type: "gemini", APIKey: "cipher-g", Models: []string{"gemini-y"}}); err != nil {
		t.Fatalf("CreateKey(gemini) error: %v", err)
	}

	got, err := store.FindByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("FindByID error: %v", err)
	}
	types := map[string]string{}
	for _, k := range got.Keys {
		types[k.Type] = k.APIKey
		if k.ChannelID != created.ID {
			t.Fatalf("key.ChannelID = %d, want %d", k.ChannelID, created.ID)
		}
		if k.BaseURL != "https://custom.example.com" {
			t.Fatalf("key.BaseURL = %q, want channel base_url", k.BaseURL)
		}
	}
	if types["anthropic"] != "cipher-a" || types["gemini"] != "cipher-g" {
		t.Fatalf("keys types/cipher mismatch: %+v", types)
	}

	// 单把 key partial 更新：改类型不动其它 key。
	keyID := got.Keys[0].ID
	if _, err := store.UpdateKey(ctx, keyID, appchannel.KeyInput{Models: []string{"claude-z"}}); err != nil {
		t.Fatalf("UpdateKey error: %v", err)
	}
	updated, _ := store.FindKeyByID(ctx, keyID)
	if len(updated.Models) != 1 || updated.Models[0] != "claude-z" {
		t.Fatalf("UpdateKey models = %v, want [claude-z]", updated.Models)
	}
}
