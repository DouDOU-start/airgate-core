package cursor

import (
	"testing"
	"time"
)

func TestConversationStateManagerReuseAndAccountIsolation(t *testing.T) {
	m := newConversationStateManager()
	first := m.acquire("session-1", "account-a")
	if first == nil {
		t.Fatal("首次 acquire 不应为空")
	}
	id := first.conversationID()
	first.Release()

	reused := m.acquire("session-1", "account-a")
	if reused == nil || reused.conversationID() != id {
		t.Fatalf("同账号同会话未复用 conversationId: first=%q reused=%q", id, reused.conversationID())
	}
	reused.Release()

	otherAccount := m.acquire("session-1", "account-b")
	if otherAccount == nil || otherAccount.conversationID() == id {
		t.Fatalf("不同账号不应复用 conversationId: first=%q other=%q", id, otherAccount.conversationID())
	}
	otherAccount.Release()
}

func TestConversationStateManagerTTLAndConcurrentLocking(t *testing.T) {
	m := newConversationStateManager()
	m.ttl = 10 * time.Millisecond
	first := m.acquire("session-ttl", "account-a")
	id := first.conversationID()
	first.Release()
	time.Sleep(20 * time.Millisecond)
	expired := m.acquire("session-ttl", "account-a")
	if expired == nil || expired.conversationID() == id {
		t.Fatalf("TTL 过期后应新建会话: old=%q new=%q", id, expired.conversationID())
	}
	expired.Release()

	held := m.acquire("same", "account-a")
	acquiredSame := make(chan *conversationLease, 1)
	go func() { acquiredSame <- m.acquire("same", "account-a") }()
	select {
	case lease := <-acquiredSame:
		lease.Release()
		t.Fatal("同一会话在首个 lease 未释放前不应并发获得锁")
	case <-time.After(30 * time.Millisecond):
	}
	held.Release()
	select {
	case lease := <-acquiredSame:
		lease.Release()
	case <-time.After(time.Second):
		t.Fatal("释放首个 lease 后，同一会话仍未获得锁")
	}

	// 不同会话的 acquire 不应被上面同会话的锁等待拖住。
	held = m.acquire("blocked", "account-a")
	acquiredOther := make(chan *conversationLease, 1)
	go func() { acquiredOther <- m.acquire("other", "account-a") }()
	select {
	case lease := <-acquiredOther:
		lease.Release()
	case <-time.After(200 * time.Millisecond):
		t.Fatal("不同会话 acquire 被错误地阻塞")
	}
	held.Release()
}
