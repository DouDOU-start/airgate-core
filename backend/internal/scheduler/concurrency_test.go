package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestRedis 启动进程内 miniredis 并返回客户端。
func newTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

// TestConcurrencyAcquireReleaseSemantics 上限内可获取、满则拒绝、释放后可再获取。
func TestConcurrencyAcquireReleaseSemantics(t *testing.T) {
	_, rdb := newTestRedis(t)
	cm := NewConcurrencyManager(rdb)
	ctx := context.Background()

	if err := cm.AcquireKeySlot(ctx, 1, "r1", 2, time.Minute); err != nil {
		t.Fatalf("第 1 个槽位获取失败: %v", err)
	}
	if err := cm.AcquireKeySlot(ctx, 1, "r2", 2, time.Minute); err != nil {
		t.Fatalf("第 2 个槽位获取失败: %v", err)
	}
	if err := cm.AcquireKeySlot(ctx, 1, "r3", 2, time.Minute); !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("超上限期望 ErrConcurrencyLimit，实际 %v", err)
	}

	cm.ReleaseKeySlot(ctx, 1, "r1")
	if err := cm.AcquireKeySlot(ctx, 1, "r3", 2, time.Minute); err != nil {
		t.Fatalf("释放后应可再获取: %v", err)
	}

	// 不同渠道的槽位互相隔离。
	if err := cm.AcquireKeySlot(ctx, 2, "r4", 1, time.Minute); err != nil {
		t.Fatalf("其他渠道不应受影响: %v", err)
	}
}

// TestConcurrencyZombieCleanup score（过期时刻）早于当前的僵尸 slot 在 acquire 时被清理，
// 不永久占用并发额度。
func TestConcurrencyZombieCleanup(t *testing.T) {
	_, rdb := newTestRedis(t)
	cm := NewConcurrencyManager(rdb)
	ctx := context.Background()

	// 直接种一个已过期的僵尸 slot（模拟进程崩溃后 Release 没跑）。
	key := keyConcurrencyKey(1)
	if err := rdb.ZAdd(ctx, key, redis.Z{
		Score:  float64(time.Now().Unix() - 10),
		Member: "zombie",
	}).Err(); err != nil {
		t.Fatalf("种僵尸 slot 失败: %v", err)
	}

	// max=1：若僵尸未被清理则此处必被拒绝。
	if err := cm.AcquireKeySlot(ctx, 1, "r1", 1, time.Minute); err != nil {
		t.Fatalf("僵尸 slot 应被清理后放行: %v", err)
	}
	if n, _ := rdb.ZScore(ctx, key, "zombie").Result(); n != 0 {
		t.Fatalf("僵尸成员应已被删除，score=%v", n)
	}
}

// TestConcurrencyKeyTTL 整 key 兜底 TTL 随 acquire 设置，且只增不减
// （短 TTL 的 acquire 不得缩短长流 slot 的存活窗口）。
func TestConcurrencyKeyTTL(t *testing.T) {
	mr, rdb := newTestRedis(t)
	cm := NewConcurrencyManager(rdb)
	ctx := context.Background()

	if err := cm.AcquireKeySlot(ctx, 1, "long", 10, 30*time.Minute); err != nil {
		t.Fatalf("获取长 TTL 槽位失败: %v", err)
	}
	key := keyConcurrencyKey(1)
	longTTL := mr.TTL(key)
	if longTTL <= 0 {
		t.Fatalf("key 应有兜底 TTL，实际 %v", longTTL)
	}

	// 短 TTL 的 acquire 不缩短整 key TTL。
	if err := cm.AcquireKeySlot(ctx, 1, "short", 10, time.Minute); err != nil {
		t.Fatalf("获取短 TTL 槽位失败: %v", err)
	}
	if got := mr.TTL(key); got < longTTL {
		t.Fatalf("整 key TTL 被缩短：%v < %v", got, longTTL)
	}
}

// TestConcurrencyUnlimitedBypass 用户/Key 级槽位在 max<=0 时直接放行且零 Redis 写入
// （热路径优化）；渠道级 max<=0 仍记录 slot（管理端观测口径）。
func TestConcurrencyUnlimitedBypass(t *testing.T) {
	_, rdb := newTestRedis(t)
	cm := NewConcurrencyManager(rdb)
	ctx := context.Background()

	if err := cm.AcquireUserSlot(ctx, 1, "r1", 0, time.Minute); err != nil {
		t.Fatalf("不限并发应放行: %v", err)
	}
	if n, _ := rdb.Exists(ctx, userConcurrencyKey(1)).Result(); n != 0 {
		t.Fatal("用户级不限并发不应写入 Redis")
	}

	if err := cm.AcquireKeySlot(ctx, 1, "r2", 0, time.Minute); err != nil {
		t.Fatalf("渠道级不限并发应放行: %v", err)
	}
	if n, _ := rdb.ZCard(ctx, keyConcurrencyKey(1)).Result(); n != 1 {
		t.Fatalf("渠道级不限并发仍应记录 slot（观测口径），实际 %d 个", n)
	}
}

// TestConcurrencyFailOpen Redis 不可用时 fail-open 放行（不阻断主链路）。
func TestConcurrencyFailOpen(t *testing.T) {
	mr, rdb := newTestRedis(t)
	cm := NewConcurrencyManager(rdb)
	mr.Close() // 模拟 Redis 故障

	if err := cm.AcquireKeySlot(context.Background(), 1, "r1", 1, time.Minute); err != nil {
		t.Fatalf("Redis 故障应 fail-open 放行: %v", err)
	}
}

// TestRPMTryIncrementAtomicLimit tryIncrement 原子判增：达到上限拒绝且不递增。
func TestRPMTryIncrementAtomicLimit(t *testing.T) {
	_, rdb := newTestRedis(t)
	r := NewRPMCounter(rdb)
	ctx := context.Background()

	var minute int64
	for i := 0; i < 2; i++ {
		ok, m, err := r.TryIncrementKeyRPM(ctx, 1, 2)
		if err != nil || !ok {
			t.Fatalf("第 %d 次递增应放行: ok=%v err=%v", i+1, ok, err)
		}
		minute = m
	}
	ok, _, err := r.TryIncrementKeyRPM(ctx, 1, 2)
	if err != nil {
		t.Fatalf("TryIncrementKeyRPM err = %v", err)
	}
	if ok {
		t.Fatal("达到上限应拒绝")
	}
	// 被拒的尝试不应递增计数。
	if got, _ := rdb.Get(ctx, keyMinuteKey(1, minute)).Int(); got != 2 {
		t.Fatalf("计数 = %d, 期望 2（拒绝不递增）", got)
	}

	// 回退后可再次放行。
	r.DecrementKeyRPM(ctx, 1, minute)
	if ok, _, _ := r.TryIncrementKeyRPM(ctx, 1, 2); !ok {
		t.Fatal("回退后应可再次放行")
	}
}

// TestRPMTryIncrementSetsTTL 递增创建的分钟 key 必须带 TTL（防 Redis 内存泄漏）。
func TestRPMTryIncrementSetsTTL(t *testing.T) {
	mr, rdb := newTestRedis(t)
	r := NewRPMCounter(rdb)

	_, minute, err := r.TryIncrementKeyRPM(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("TryIncrementKeyRPM err = %v", err)
	}
	if ttl := mr.TTL(keyMinuteKey(1, minute)); ttl <= 0 {
		t.Fatalf("分钟 key 应带 TTL，实际 %v", ttl)
	}
}

// TestRPMDecrementOnlyIfExists decrement 仅在 key 存在时递减，
// 不创建值为 -1 的无 TTL key（跨分钟窗口回退场景）。
func TestRPMDecrementOnlyIfExists(t *testing.T) {
	_, rdb := newTestRedis(t)
	r := NewRPMCounter(rdb)
	ctx := context.Background()

	r.DecrementKeyRPM(ctx, 1, 12345)
	if n, _ := rdb.Exists(ctx, keyMinuteKey(1, 12345)).Result(); n != 0 {
		t.Fatal("不存在的 key 不应被 decrement 创建")
	}
}

// TestRPMFailOpen Redis 不可用时 fail-open 放行。
func TestRPMFailOpen(t *testing.T) {
	mr, rdb := newTestRedis(t)
	r := NewRPMCounter(rdb)
	mr.Close()

	ok, _, err := r.TryIncrementKeyRPM(context.Background(), 1, 1)
	if err != nil || !ok {
		t.Fatalf("Redis 故障应 fail-open 放行: ok=%v err=%v", ok, err)
	}
}
