package mailer

import (
	"strings"
	"testing"
	"time"
)

func TestSendReturnsErrorWhenSMTPNotConfigured(t *testing.T) {
	m := New(Config{})

	err := m.Send("u@test.com", "主题", "<p>内容</p>")
	if err == nil {
		t.Fatal("未配置 SMTP 时应返回错误")
	}
	if !strings.Contains(err.Error(), "SMTP 未配置") {
		t.Fatalf("错误 = %v，期望提示 SMTP 未配置", err)
	}
}

func TestVerifyCodeStoreGenerateCheckAndVerify(t *testing.T) {
	store := NewVerifyCodeStore()

	code := store.Generate("u@test.com")
	if len(code) != 6 {
		t.Fatalf("验证码长度 = %d，期望 6", len(code))
	}
	for _, ch := range code {
		if ch < '0' || ch > '9' {
			t.Fatalf("验证码包含非数字字符: %q", code)
		}
	}
	if !store.Check("u@test.com", code) {
		t.Fatal("生成的验证码应可通过 Check")
	}
	if !store.Verify("u@test.com", code) {
		t.Fatal("生成的验证码应可通过 Verify")
	}
	if store.Check("u@test.com", code) {
		t.Fatal("Verify 成功后验证码应被删除")
	}
}

func TestVerifyCodeStoreRejectsAndCleansExpiredCode(t *testing.T) {
	store := NewVerifyCodeStore()
	store.mu.Lock()
	store.codes["u@test.com"] = codeEntry{
		code:      "123456",
		expiresAt: time.Now().Add(-time.Minute),
	}
	store.mu.Unlock()

	if store.Check("u@test.com", "123456") {
		t.Fatal("过期验证码不应通过 Check")
	}
	store.mu.RLock()
	_, exists := store.codes["u@test.com"]
	store.mu.RUnlock()
	if exists {
		t.Fatal("过期验证码应在 Check 后被删除")
	}

	store.mu.Lock()
	store.codes["old@test.com"] = codeEntry{code: "111111", expiresAt: time.Now().Add(-time.Minute)}
	store.codes["new@test.com"] = codeEntry{code: "222222", expiresAt: time.Now().Add(time.Minute)}
	store.mu.Unlock()
	store.cleanup()

	store.mu.RLock()
	_, oldExists := store.codes["old@test.com"]
	_, newExists := store.codes["new@test.com"]
	store.mu.RUnlock()
	if oldExists || !newExists {
		t.Fatalf("清理结果异常: old=%v new=%v", oldExists, newExists)
	}
}
