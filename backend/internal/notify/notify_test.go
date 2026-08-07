package notify

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type stubChannel struct {
	name string
	err  error
	got  Message
}

func (s *stubChannel) Name() string { return s.name }
func (s *stubChannel) Send(_ context.Context, msg Message) error {
	s.got = msg
	return s.err
}

func TestMultiSendAggregatesErrors(t *testing.T) {
	a := &stubChannel{name: "a"}
	b := &stubChannel{name: "b", err: errors.New("boom")}
	m := &Multi{Channels: []Channel{a, b}}
	err := m.Send(context.Background(), Message{Title: "t", Body: "body"})
	if err == nil {
		t.Fatal("应聚合错误")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("应包含 boom，got %v", err)
	}
	if a.got.Title != "t" || b.got.Body != "body" {
		t.Fatalf("两条通道都应收到消息：a=%#v b=%#v", a.got, b.got)
	}
}

func TestSplitTokens(t *testing.T) {
	got := SplitTokens("a,b；c\nd  ,a ")
	want := []string{"a", "b", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("got = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got = %#v, want %#v", got, want)
		}
	}
}
