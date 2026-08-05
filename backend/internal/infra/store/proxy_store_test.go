package store

import (
	"context"
	"testing"

	appproxy "github.com/DouDOU-start/airgate-core/internal/app/proxy"
	"github.com/DouDOU-start/airgate-core/internal/auth"
)

const proxyStoreTestSecret = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func TestProxyStoreEncryptsPasswordAtRest(t *testing.T) {
	db := enttestOpen(t)
	defer func() { _ = db.Close() }()
	store := NewProxyStore(db, proxyStoreTestSecret)
	ctx := context.Background()

	created, err := store.Create(ctx, appproxy.CreateInput{
		Name: "测试代理", Protocol: "http", Address: "127.0.0.1", Port: 8080,
		Username: "user", Password: "plain-password",
	})
	if err != nil {
		t.Fatalf("创建代理失败: %v", err)
	}
	if created.Password != "plain-password" {
		t.Fatalf("领域对象未返回解密密码: %q", created.Password)
	}

	stored, err := db.Proxy.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("读取代理记录失败: %v", err)
	}
	if stored.Password == "plain-password" || !auth.IsEncryptedSecretValue(stored.Password) {
		t.Fatalf("代理密码未加密落库: %q", stored.Password)
	}

	updatedPassword := "new-password"
	updated, err := store.Update(ctx, created.ID, appproxy.UpdateInput{Password: &updatedPassword})
	if err != nil || updated.Password != updatedPassword {
		t.Fatalf("更新代理密码失败: proxy=%+v err=%v", updated, err)
	}
	stored, err = db.Proxy.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("读取更新后的代理记录失败: %v", err)
	}
	plain, err := auth.DecryptSecretValue(stored.Password, proxyStoreTestSecret)
	if err != nil || plain != updatedPassword {
		t.Fatalf("更新后的密文无法解密: plain=%q err=%v", plain, err)
	}
}
