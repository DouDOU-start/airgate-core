package server

import (
	"testing"

	appchannel "github.com/DouDOU-start/airgate-core/internal/app/channel"
)

func TestChannelTestSnapshot保留渠道名称(t *testing.T) {
	key := appchannel.ChannelKey{
		ID:          23,
		Name:        "测试密钥",
		ChannelID:   7,
		ChannelName: "测试渠道",
		Models:      []string{"gpt-test"},
	}

	snap := channelTestSnapshot(key, "sk-test")
	if snap.ChannelID != key.ChannelID {
		t.Fatalf("渠道 ID = %d，期望 %d", snap.ChannelID, key.ChannelID)
	}
	if snap.ChannelName != key.ChannelName {
		t.Fatalf("渠道名称 = %q，期望 %q", snap.ChannelName, key.ChannelName)
	}
	if snap.KeyName != key.Name {
		t.Fatalf("密钥名称 = %q，期望 %q", snap.KeyName, key.Name)
	}
	if _, ok := snap.Models["gpt-test"]; !ok {
		t.Fatal("测试快照未保留模型列表")
	}
}
