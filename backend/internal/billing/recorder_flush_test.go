package billing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
)

// newLogicRecorder 构造只测 flush 逻辑的 Recorder（不接 DB，注入落库函数与无睡眠）。
func newLogicRecorder() *Recorder {
	r := NewRecorder(nil, 1)
	r.sleep = func(time.Duration) {}
	return r
}

// TestFlushDegradesToPerRecordInsert 整批失败重试耗尽后降级逐条；
// 单条 channel FK 违约再降级为去掉 channel 边重插——计费金额与扣款绝不丢。
func TestFlushDegradesToPerRecordInsert(t *testing.T) {
	batch := []UsageRecord{
		{UserID: 1, ChannelID: 42, Model: "gpt-4o", ActualCost: 0.1},
		{UserID: 2, ChannelID: 43, Model: "gpt-4o", ActualCost: 0.2},
		{UserID: 3, ChannelID: 44, Model: "gpt-4o", ActualCost: 0.3},
	}

	type oneCall struct {
		userID      int
		withChannel bool
	}

	// constraintErr 模拟 ent 对 FK 违约（23503）的包装（wrap 后 errors.As 仍可识别）。
	constraintErr := fmt.Errorf("插入 UsageLog 失败: %w", &ent.ConstraintError{})

	cases := []struct {
		name string
		// failOneWithChannel 指定 insertOne(withChannel=true) 的失败错误（按 UserID）。
		failOneWithChannel map[int]error
		// failOneAlways 指定去掉 channel 边后仍失败的 UserID 集合。
		failOneAlways map[int]bool
		wantOneCalls  []oneCall
	}{
		{
			name: "整批失败但逐条全成功（瞬时故障）",
			wantOneCalls: []oneCall{
				{1, true}, {2, true}, {3, true},
			},
		},
		{
			name:               "单条 FK 违约去掉 channel 边重插，其余不受影响",
			failOneWithChannel: map[int]error{2: constraintErr},
			wantOneCalls: []oneCall{
				{1, true}, {2, true}, {2, false}, {3, true},
			},
		},
		{
			name:               "去掉 channel 边仍失败才真正丢弃该条，其余照常入账",
			failOneWithChannel: map[int]error{2: constraintErr},
			failOneAlways:      map[int]bool{2: true},
			wantOneCalls: []oneCall{
				{1, true}, {2, true}, {2, false}, {3, true},
			},
		},
		{
			name:               "非约束类失败不清 channel 边（保留归属，不盲目重插）",
			failOneWithChannel: map[int]error{2: errors.New("connection reset")},
			wantOneCalls: []oneCall{
				{1, true}, {2, true}, {3, true},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newLogicRecorder()

			batchCalls := 0
			r.insertBatch = func(context.Context, []UsageRecord) error {
				batchCalls++
				return errors.New("FK violation: usage_logs_channels_usage_logs")
			}

			var oneCalls []oneCall
			r.insertOne = func(_ context.Context, rec UsageRecord, withChannel bool) error {
				oneCalls = append(oneCalls, oneCall{rec.UserID, withChannel})
				if withChannel {
					if err := tc.failOneWithChannel[rec.UserID]; err != nil {
						return err
					}
					return nil
				}
				// 去掉 channel 边的重插路径。
				if tc.failOneAlways[rec.UserID] {
					return fmt.Errorf("still failing user=%d", rec.UserID)
				}
				return nil
			}

			r.flush(context.Background(), batch)

			if batchCalls != maxRetries {
				t.Errorf("整批重试次数 = %d, want %d", batchCalls, maxRetries)
			}
			if len(oneCalls) != len(tc.wantOneCalls) {
				t.Fatalf("逐条调用 = %+v, want %+v", oneCalls, tc.wantOneCalls)
			}
			for i, want := range tc.wantOneCalls {
				if oneCalls[i] != want {
					t.Errorf("逐条调用[%d] = %+v, want %+v", i, oneCalls[i], want)
				}
			}
		})
	}
}

// TestFlushBatchSuccessSkipsFallback 整批成功不进入降级路径。
func TestFlushBatchSuccessSkipsFallback(t *testing.T) {
	r := newLogicRecorder()
	batchCalls := 0
	r.insertBatch = func(context.Context, []UsageRecord) error {
		batchCalls++
		return nil
	}
	r.insertOne = func(context.Context, UsageRecord, bool) error {
		t.Fatal("整批成功不应逐条插入")
		return nil
	}

	r.flush(context.Background(), []UsageRecord{{UserID: 1, ChannelID: 42}})
	if batchCalls != 1 {
		t.Errorf("insertBatch 调用次数 = %d, want 1", batchCalls)
	}
}

// TestRecordBufferFullDropsNonBlocking 缓冲满时 Record 丢弃该条且立即返回（非阻塞），
// 已入队记录不受影响。
func TestRecordBufferFullDropsNonBlocking(t *testing.T) {
	r := NewRecorder(nil, 1) // 缓冲 1，不 Start：第二条必然触发满缓冲路径

	r.Record(UsageRecord{UserID: 1, Model: "gpt-4o"})
	done := make(chan struct{})
	go func() {
		r.Record(UsageRecord{UserID: 2, Model: "gpt-4o", TotalCost: 0.5})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("缓冲满时 Record 应立即返回，不阻塞")
	}

	if got := len(r.ch); got != 1 {
		t.Fatalf("缓冲内记录数 = %d, 期望 1（第二条被丢弃）", got)
	}
	rec := <-r.ch
	if rec.UserID != 1 {
		t.Fatalf("保留的应是先入队的记录，实际 user_id=%d", rec.UserID)
	}
}

// TestStopDrainsBuffer Stop 排空缓冲：Stop 返回前所有已提交记录均已落库，
// 且重复 Stop 幂等不 panic。
func TestStopDrainsBuffer(t *testing.T) {
	r := newLogicRecorder()
	r.ch = make(chan UsageRecord, 10)

	var mu sync.Mutex
	var flushed []UsageRecord
	r.insertBatch = func(_ context.Context, batch []UsageRecord) error {
		mu.Lock()
		flushed = append(flushed, batch...)
		mu.Unlock()
		return nil
	}

	for i := 1; i <= 3; i++ {
		r.Record(UsageRecord{UserID: i, Model: "gpt-4o"})
	}
	r.Start()
	r.Stop()
	r.Stop() // 幂等

	mu.Lock()
	defer mu.Unlock()
	if len(flushed) != 3 {
		t.Fatalf("Stop 后落库记录数 = %d, 期望 3（缓冲须排空）", len(flushed))
	}
}

// TestFlushBatchRetryThenSuccess 整批第二次重试成功即返回，不降级。
func TestFlushBatchRetryThenSuccess(t *testing.T) {
	r := newLogicRecorder()
	batchCalls := 0
	r.insertBatch = func(context.Context, []UsageRecord) error {
		batchCalls++
		if batchCalls == 1 {
			return errors.New("transient")
		}
		return nil
	}
	r.insertOne = func(context.Context, UsageRecord, bool) error {
		t.Fatal("重试成功不应逐条插入")
		return nil
	}

	r.flush(context.Background(), []UsageRecord{{UserID: 1}})
	if batchCalls != 2 {
		t.Errorf("insertBatch 调用次数 = %d, want 2", batchCalls)
	}
}
