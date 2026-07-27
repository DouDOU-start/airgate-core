package store

import (
	"context"
	"encoding/json"
	"testing"

	entupstreamrequestlog "github.com/DouDOU-start/airgate-core/ent/upstreamrequestlog"
	appupstreamlog "github.com/DouDOU-start/airgate-core/internal/app/upstreamlog"
	"github.com/DouDOU-start/airgate-core/internal/errlog"
)

func TestUpstreamLogStore补充历史渠道名称(t *testing.T) {
	db := enttestOpen(t)
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	channel := db.Channel.Create().
		SetName("历史渠道").
		SetBaseURL("https://example.com").
		SaveX(ctx)
	channelKey := db.ChannelKey.Create().
		SetChannelID(channel.ID).
		SetName("历史密钥").
		SetType("openai_compatible").
		SetAPIKey("cipher").
		SaveX(ctx)
	oldChain, err := json.Marshal([]errlog.AttemptHop{{
		Seq:         1,
		ChannelID:   channel.ID,
		ChannelName: channel.Name,
		KeyID:       channelKey.ID,
	}})
	if err != nil {
		t.Fatalf("构造历史重试链失败：%v", err)
	}
	db.UpstreamRequestLog.Create().
		SetRequestID("req-history-channel-name").
		SetSource(entupstreamrequestlog.SourceChannelTest).
		SetChannelID(channel.ID).
		SetAttemptChain(oldChain).
		SaveX(ctx)
	db.UpstreamRequestLog.Create().
		SetRequestID("req-channel-name-snapshot").
		SetSource(entupstreamrequestlog.SourceChannelTest).
		SetChannelID(channel.ID).
		SetChannelName("请求时渠道名").
		SaveX(ctx)

	items, err := NewUpstreamLogStore(db).List(ctx, appupstreamlog.ListFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("查询失败记录失败：%v", err)
	}
	if len(items) != 2 {
		t.Fatalf("失败记录数量 = %d，期望 2", len(items))
	}
	byRequestID := make(map[string]string, len(items))
	for _, item := range items {
		byRequestID[item.RequestID] = item.ChannelName
	}
	if got := byRequestID["req-history-channel-name"]; got != channel.Name {
		t.Fatalf("历史记录渠道名称 = %q，期望 %q", got, channel.Name)
	}
	if got := byRequestID["req-channel-name-snapshot"]; got != "请求时渠道名" {
		t.Fatalf("渠道名称快照 = %q，期望保留请求时名称", got)
	}
	var hops []errlog.AttemptHop
	for _, item := range items {
		if item.RequestID == "req-history-channel-name" {
			if err := json.Unmarshal(item.AttemptChain, &hops); err != nil {
				t.Fatalf("解析补充后的重试链失败：%v", err)
			}
		}
	}
	if len(hops) != 1 || hops[0].KeyName != channelKey.Name {
		t.Fatalf("历史重试链密钥名称补充异常：%+v", hops)
	}
}
