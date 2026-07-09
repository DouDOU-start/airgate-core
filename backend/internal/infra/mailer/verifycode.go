package mailer

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

// VerifyCodeStore 验证码内存存储。
type VerifyCodeStore struct {
	mu    sync.RWMutex
	codes map[string]codeEntry
}

type codeEntry struct {
	code      string
	expiresAt time.Time
}

// NewVerifyCodeStore 创建验证码存储。
func NewVerifyCodeStore() *VerifyCodeStore {
	s := &VerifyCodeStore{codes: make(map[string]codeEntry)}
	// 后台清理过期条目
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		for range ticker.C {
			s.cleanup()
		}
	}()
	return s
}

// Generate 为指定邮箱生成 6 位验证码，有效期 10 分钟。
func (s *VerifyCodeStore) Generate(email string) string {
	code := randomCode()
	s.mu.Lock()
	s.codes[email] = codeEntry{
		code:      code,
		expiresAt: time.Now().Add(10 * time.Minute),
	}
	s.mu.Unlock()
	return code
}

// Check 校验验证码，但不消耗验证码。
func (s *VerifyCodeStore) Check(email, code string) bool {
	s.mu.RLock()
	entry, ok := s.codes[email]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().After(entry.expiresAt) {
		s.mu.Lock()
		if current, exists := s.codes[email]; exists && current.expiresAt.Equal(entry.expiresAt) {
			delete(s.codes, email)
		}
		s.mu.Unlock()
		return false
	}
	return entry.code == code
}

// Verify 校验验证码，成功后自动删除。
func (s *VerifyCodeStore) Verify(email, code string) bool {
	if !s.Check(email, code) {
		return false
	}
	s.mu.Lock()
	delete(s.codes, email)
	s.mu.Unlock()
	return true
}

func (s *VerifyCodeStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.codes {
		if now.After(v.expiresAt) {
			delete(s.codes, k)
		}
	}
}

// randomCode 生成 6 位数字验证码。取 4 字节随机数对 1e6 取模：
// 2^32 对 1e6 的余数偏差约为 1/4295（此前 3 字节截断前 6 位的写法分布明显有偏）。
func randomCode() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	n := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	return fmt.Sprintf("%06d", n%1_000_000)
}
