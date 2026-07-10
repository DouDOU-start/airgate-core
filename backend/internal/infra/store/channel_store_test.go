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
		SetType("openai_compatible").
		SetBaseURL("https://upstream.example.com").
		Save(context.Background())
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return ch
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
	chIdle := createTestChannel(t, db, "channel-idle")

	now := time.Now()
	todayStart := now.Add(-time.Hour)
	yesterday := now.Add(-2 * time.Hour)

	// chA：今日两条，成本 = 10×1.5 + 4×2 = 23，收益 = 8 + 3 = 11。
	// chB：今日一条（成本 5×0.5 = 2.5，收益 6）+ 今日之前一条（成本 8×1 = 8，收益 2）。
	fixtures := []struct {
		channelID  int
		totalCost  float64
		accountRM  float64
		actualCost float64
		createdAt  time.Time
	}{
		{chA.ID, 10, 1.5, 8, now},
		{chA.ID, 4, 2, 3, now},
		{chB.ID, 5, 0.5, 6, now},
		{chB.ID, 8, 1, 2, yesterday},
	}
	for _, item := range fixtures {
		if _, err := db.UsageLog.Create().
			SetModel("gpt-5").
			SetChannelID(item.channelID).
			SetTotalCost(item.totalCost).
			SetAccountRateMultiplier(item.accountRM).
			SetActualCost(item.actualCost).
			SetCreatedAt(item.createdAt).
			Save(ctx); err != nil {
			t.Fatalf("create usage log: %v", err)
		}
	}

	store := NewChannelStore(db)
	stats, err := store.GetChannelMoneyStats(ctx, []int{chA.ID, chB.ID, chIdle.ID}, todayStart)
	if err != nil {
		t.Fatalf("GetChannelMoneyStats returned error: %v", err)
	}

	assertMoney := func(name string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-9 {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
	assertMoney("chA.Cost", stats[chA.ID].Cost, 23)
	assertMoney("chA.Revenue", stats[chA.ID].Revenue, 11)
	assertMoney("chA.TodayCost", stats[chA.ID].TodayCost, 23)
	assertMoney("chA.TodayRevenue", stats[chA.ID].TodayRevenue, 11)
	assertMoney("chB.Cost", stats[chB.ID].Cost, 10.5)
	assertMoney("chB.Revenue", stats[chB.ID].Revenue, 8)
	assertMoney("chB.TodayCost", stats[chB.ID].TodayCost, 2.5)
	assertMoney("chB.TodayRevenue", stats[chB.ID].TodayRevenue, 6)

	// 无用量渠道不出现在聚合结果里，零值由调用方兜底。
	if _, ok := stats[chIdle.ID]; ok {
		t.Fatalf("idle channel should not appear in stats")
	}

	// 空入参：返回空表不查库。
	empty, err := store.GetChannelMoneyStats(ctx, nil, todayStart)
	if err != nil {
		t.Fatalf("GetChannelMoneyStats(nil) returned error: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("GetChannelMoneyStats(nil) = %v, want empty", empty)
	}
}

// TestChannelStoreCreateCustomType custom 渠道类型可正常落库
// （此前 schema 枚举缺 "custom"，dto/registry/adaptor 支持但 ent 校验拒绝）。
func TestChannelStoreCreateCustomType(t *testing.T) {
	db := enttestOpen(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()

	ctx := context.Background()
	store := NewChannelStore(db)

	created, err := store.Create(ctx, appchannel.CreateInput{
		Name:    "custom-upstream",
		Type:    "custom",
		BaseURL: "https://custom.example.com",
		APIKeys: []string{"cipher-1"},
		Models:  []string{"my-model"},
	})
	if err != nil {
		t.Fatalf("Create(custom) returned error: %v", err)
	}
	if created.Type != "custom" {
		t.Fatalf("created.Type = %q, want custom", created.Type)
	}

	got, err := store.FindByID(ctx, created.ID)
	if err != nil || got.Type != "custom" {
		t.Fatalf("FindByID = (%+v, %v), want type custom", got, err)
	}
}
