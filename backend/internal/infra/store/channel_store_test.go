package store

import (
	"context"
	"math"
	"testing"

	"github.com/DouDOU-start/airgate-core/ent"
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

	// chA：两条记录，成本 = 10×1.5 + 4×2 = 23，收益 = 8 + 3 = 11。
	// chB：一条记录，成本 = 5×0.5 = 2.5，收益 = 6。
	fixtures := []struct {
		channelID  int
		totalCost  float64
		accountRM  float64
		actualCost float64
	}{
		{chA.ID, 10, 1.5, 8},
		{chA.ID, 4, 2, 3},
		{chB.ID, 5, 0.5, 6},
	}
	for _, item := range fixtures {
		if _, err := db.UsageLog.Create().
			SetModel("gpt-5").
			SetChannelID(item.channelID).
			SetTotalCost(item.totalCost).
			SetAccountRateMultiplier(item.accountRM).
			SetActualCost(item.actualCost).
			Save(ctx); err != nil {
			t.Fatalf("create usage log: %v", err)
		}
	}

	store := NewChannelStore(db)
	stats, err := store.GetChannelMoneyStats(ctx, []int{chA.ID, chB.ID, chIdle.ID})
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
	assertMoney("chB.Cost", stats[chB.ID].Cost, 2.5)
	assertMoney("chB.Revenue", stats[chB.ID].Revenue, 6)

	// 无用量渠道不出现在聚合结果里，零值由调用方兜底。
	if _, ok := stats[chIdle.ID]; ok {
		t.Fatalf("idle channel should not appear in stats")
	}

	// 空入参：返回空表不查库。
	empty, err := store.GetChannelMoneyStats(ctx, nil)
	if err != nil {
		t.Fatalf("GetChannelMoneyStats(nil) returned error: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("GetChannelMoneyStats(nil) = %v, want empty", empty)
	}
}
