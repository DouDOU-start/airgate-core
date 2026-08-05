package store

import (
	"context"
	"testing"
	"time"

	relaytask "github.com/DouDOU-start/airgate-core/internal/relay/task"
)

// newTaskRow 最小合法任务行。
func newTaskRow(taskID string, userID int) *relaytask.Task {
	return &relaytask.Task{
		TaskID:         taskID,
		Platform:       relaytask.PlatformOpenAIVideo,
		Action:         "generate",
		Status:         relaytask.StatusSubmitted,
		RequestModel:   "sora-2",
		HoldAmount:     0.8,
		EstTotal:       0.4,
		RateMultiplier: 2.0,
		Resolution:     "1080p",
		SubmitTime:     time.Now(),
		UserID:         userID,
		ChannelID:      1,
	}
}

func TestTaskStoreCRUDAndCAS(t *testing.T) {
	client := enttestOpen(t)
	defer func() { _ = client.Close() }()
	s := NewTaskStore(client)
	ctx := context.Background()

	id, err := s.Insert(ctx, newTaskRow("video_1", 22))
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	t.Run("GetForUser 命中与归属隔离", func(t *testing.T) {
		got, err := s.GetForUser(ctx, relaytask.PlatformOpenAIVideo, "video_1", 22)
		if err != nil || got == nil || got.ID != id || got.HoldAmount != 0.8 || got.Resolution != "1080p" {
			t.Fatalf("got = %+v, err = %v", got, err)
		}
		// 非本人 / 非本平台 → nil
		if got, _ := s.GetForUser(ctx, relaytask.PlatformOpenAIVideo, "video_1", 99); got != nil {
			t.Error("跨用户不应命中")
		}
		if got, _ := s.GetForUser(ctx, relaytask.PlatformSuno, "video_1", 22); got != nil {
			t.Error("跨平台不应命中")
		}
	})

	t.Run("未终态扫描与计数", func(t *testing.T) {
		n, err := s.CountUnfinished(ctx)
		if err != nil || n != 1 {
			t.Fatalf("count = %d, err = %v", n, err)
		}
		list, err := s.ListUnfinished(ctx, 10)
		if err != nil || len(list) != 1 {
			t.Fatalf("list = %d, err = %v", len(list), err)
		}
	})

	t.Run("UpdateStatusCAS 未终态可更新", func(t *testing.T) {
		now := time.Now()
		applied, err := s.UpdateStatusCAS(ctx, id, relaytask.StatusUpdate{
			Status: relaytask.StatusSuccess, Progress: 100, Seconds: 8,
			Data: []byte(`{"id":"video_1"}`), FinishTime: &now,
		})
		if err != nil || !applied {
			t.Fatalf("applied = %v, err = %v", applied, err)
		}
		got, _ := s.GetForUser(ctx, relaytask.PlatformOpenAIVideo, "video_1", 22)
		if got.Status != relaytask.StatusSuccess || got.Seconds != 8 || got.FinishTime == nil {
			t.Fatalf("got = %+v", got)
		}
	})

	t.Run("UpdateStatusCAS 终态后拒绝覆盖", func(t *testing.T) {
		applied, err := s.UpdateStatusCAS(ctx, id, relaytask.StatusUpdate{
			Status: relaytask.StatusFailure, FailReason: "覆盖尝试",
		})
		if err != nil || applied {
			t.Fatalf("终态行不应被更新: applied = %v, err = %v", applied, err)
		}
	})

	t.Run("MarkSettled 幂等闸", func(t *testing.T) {
		ok, err := s.MarkSettled(ctx, id)
		if err != nil || !ok {
			t.Fatalf("首次置位失败: %v %v", ok, err)
		}
		ok, err = s.MarkSettled(ctx, id)
		if err != nil || ok {
			t.Fatalf("重复置位应返回 false: %v %v", ok, err)
		}
	})

	t.Run("ListForUser 批量命中", func(t *testing.T) {
		if _, err := s.Insert(ctx, newTaskRow("video_2", 22)); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		list, err := s.ListForUser(ctx, relaytask.PlatformOpenAIVideo, []string{"video_1", "video_2", "missing"}, 22)
		if err != nil || len(list) != 2 {
			t.Fatalf("list = %d, err = %v", len(list), err)
		}
	})
}
