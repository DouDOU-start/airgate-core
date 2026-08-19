package cursor

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	agentpb "github.com/DouDOU-start/airgate-core/internal/relay/cursor/proto/agentpb"
)

const (
	// 默认会话状态保留一小时；TTL 只影响内存中的 Cursor 辅助状态，不影响
	// 客户端自己的 Claude Code transcript。
	defaultConversationSessionTTL   = time.Hour
	defaultConversationSessionLimit = 8192
)

// conversationState 保存一个下游会话在 Cursor 侧需要跨请求复用的状态。
// mu 覆盖整次 Run，防止同一 Claude Code 会话的并发请求同时修改 BlobStore。
type conversationState struct {
	mu sync.Mutex
	// metaMu 保护会话元数据。会话本身由 mu 独占，但 TTL 清理会在不持有
	// 会话锁时读取 lastUsed，因此不能直接无锁访问。
	metaMu sync.Mutex

	conversationID string
	store          BlobStore
	checkpoint     *agentpb.ConversationStateStructure
	lastUsed       time.Time
	inUse          int
}

// conversationLease 表示一次独占使用。Run 完成后必须 Release，否则同一会话
// 的后续请求会一直等待；OnCheckpoint 只保存服务端发来的最新快照。
type conversationLease struct {
	manager *conversationStateManager
	state   *conversationState
	once    sync.Once
}

func (l *conversationLease) conversationID() string {
	if l == nil || l.state == nil {
		return ""
	}
	return l.state.conversationID
}

func (l *conversationLease) store() BlobStore {
	if l == nil || l.state == nil {
		return nil
	}
	return l.state.store
}

func (l *conversationLease) checkpoint() *agentpb.ConversationStateStructure {
	if l == nil || l.state == nil || l.state.checkpoint == nil {
		return nil
	}
	return proto.Clone(l.state.checkpoint).(*agentpb.ConversationStateStructure)
}

func (l *conversationLease) updateCheckpoint(checkpoint *agentpb.ConversationStateStructure) {
	if l == nil || l.state == nil || checkpoint == nil {
		return
	}
	l.state.checkpoint = proto.Clone(checkpoint).(*agentpb.ConversationStateStructure)
	l.state.touch()
}

func (l *conversationLease) Release() {
	if l == nil || l.state == nil {
		return
	}
	l.once.Do(func() {
		l.state.mu.Unlock()
		if l.manager != nil {
			l.manager.mu.Lock()
			l.state.decrementUseAndTouch()
			l.manager.mu.Unlock()
		} else {
			l.state.touch()
		}
	})
}

func (s *conversationState) touch() {
	if s == nil {
		return
	}
	s.metaMu.Lock()
	s.lastUsed = time.Now()
	s.metaMu.Unlock()
}

func (s *conversationState) metadata() (lastUsed time.Time, inUse int) {
	if s == nil {
		return time.Time{}, 0
	}
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	return s.lastUsed, s.inUse
}

func (s *conversationState) incrementUse() {
	s.metaMu.Lock()
	s.inUse++
	s.lastUsed = time.Now()
	s.metaMu.Unlock()
}

func (s *conversationState) decrementUseAndTouch() {
	s.metaMu.Lock()
	if s.inUse > 0 {
		s.inUse--
	}
	s.lastUsed = time.Now()
	s.metaMu.Unlock()
}

// conversationStateManager 是 Executor 级别的内存状态缓存。key 由下游会话
// 标识构成，accountID 作为额外隔离层，避免 Cursor 账号切换后复用另一账号的
// conversationId、文件状态或 checkpoint。
type conversationStateManager struct {
	mu      sync.Mutex
	entries map[string]*conversationState
	ttl     time.Duration
	limit   int
}

func newConversationStateManager() *conversationStateManager {
	return &conversationStateManager{
		entries: make(map[string]*conversationState),
		ttl:     defaultConversationSessionTTL,
		limit:   defaultConversationSessionLimit,
	}
}

func (m *conversationStateManager) acquire(key, accountID string) *conversationLease {
	if m == nil || key == "" || accountID == "" {
		return nil
	}
	now := time.Now()
	cacheKey := accountID + "\x00" + key
	m.mu.Lock()
	if m.entries == nil {
		m.entries = make(map[string]*conversationState)
	}
	for k, entry := range m.entries {
		lastUsed, inUse := entry.metadata()
		if entry == nil || (inUse == 0 && now.Sub(lastUsed) > m.ttl) {
			delete(m.entries, k)
		}
	}
	state := m.entries[cacheKey]
	if state == nil {
		if m.limit > 0 && len(m.entries) >= m.limit {
			m.evictOldestLocked()
		}
		state = &conversationState{
			conversationID: uuid.NewString(),
			store:          NewBlobStore(),
			lastUsed:       now,
		}
		m.entries[cacheKey] = state
	}
	state.incrementUse()
	m.mu.Unlock()
	// 只在拿到会话后再等待该会话自己的锁，避免一个慢请求阻塞所有
	// 其他下游会话的查找、创建和 TTL 清理。
	state.mu.Lock()
	return &conversationLease{manager: m, state: state}
}

func (m *conversationStateManager) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	for key, entry := range m.entries {
		if entry == nil {
			oldestKey = key
			break
		}
		lastUsed, inUse := entry.metadata()
		if inUse > 0 {
			continue
		}
		if oldestKey == "" || lastUsed.Before(oldest) {
			oldestKey = key
			oldest = lastUsed
		}
	}
	if oldestKey != "" {
		delete(m.entries, oldestKey)
	}
}
