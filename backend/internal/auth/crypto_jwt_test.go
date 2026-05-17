package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testAESSecret = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func TestEncryptAndDecryptAPIKeyRoundTrip(t *testing.T) {
	encrypted, err := EncryptAPIKey("sk-明文密钥", testAESSecret)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	if encrypted == "sk-明文密钥" {
		t.Fatal("密文不应等于明文")
	}

	plain, err := DecryptAPIKey(encrypted, testAESSecret)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if plain != "sk-明文密钥" {
		t.Fatalf("明文 = %q，期望原始密钥", plain)
	}
}

func TestDecryptAPIKeyRejectsInvalidCiphertext(t *testing.T) {
	if _, err := DecryptAPIKey("不是 base64", testAESSecret); err == nil {
		t.Fatal("非法 base64 应返回错误")
	}
	if _, err := DecryptAPIKey("c2hvcnQ=", testAESSecret); err == nil {
		t.Fatal("过短密文应返回错误")
	}
	if _, err := EncryptAPIKey("sk-test", "bad-secret"); err == nil {
		t.Fatal("非法 secret 应返回错误")
	}
}

func TestJWTGenerateParseAndRefresh(t *testing.T) {
	mgr := NewJWTManager("jwt-secret", 1)
	token, err := mgr.GenerateAPIKeyToken(7, "user", "u@example.com", 11)
	if err != nil {
		t.Fatalf("签发 token 失败: %v", err)
	}

	claims, err := mgr.ParseToken(token)
	if err != nil {
		t.Fatalf("解析 token 失败: %v", err)
	}
	if claims.UserID != 7 || claims.Role != APIKeySessionRole || claims.Email != "u@example.com" || claims.APIKeyID != 11 {
		t.Fatalf("claims 异常: %+v", claims)
	}

	refreshed, err := mgr.RefreshToken(claims)
	if err != nil {
		t.Fatalf("刷新 token 失败: %v", err)
	}
	refreshedClaims, err := mgr.ParseToken(refreshed)
	if err != nil {
		t.Fatalf("解析刷新 token 失败: %v", err)
	}
	if refreshedClaims.APIKeyID != 11 {
		t.Fatalf("刷新后 APIKeyID = %d，期望 11", refreshedClaims.APIKeyID)
	}
}

func TestAPIKeyTokenAlwaysUsesAPIKeySessionRole(t *testing.T) {
	mgr := NewJWTManager("jwt-secret", 1)
	token, err := mgr.GenerateAPIKeyToken(7, "admin", "admin@example.com", 11)
	if err != nil {
		t.Fatalf("签发 token 失败: %v", err)
	}

	claims, err := mgr.ParseToken(token)
	if err != nil {
		t.Fatalf("解析 token 失败: %v", err)
	}
	if claims.Role != APIKeySessionRole {
		t.Fatalf("API Key 登录 role = %q，期望 %q", claims.Role, APIKeySessionRole)
	}
}

func TestJWTRejectsInvalidAndExpiredToken(t *testing.T) {
	mgr := NewJWTManager("jwt-secret", 1)
	if _, err := mgr.ParseToken("invalid"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("非法 token 错误 = %v，期望 ErrInvalidToken", err)
	}

	expired := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		UserID: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
			Issuer:    "airgate",
		},
	})
	token, err := expired.SignedString(mgr.secret)
	if err != nil {
		t.Fatalf("签发过期 token 失败: %v", err)
	}
	if _, err := mgr.ParseToken(token); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("过期 token 错误 = %v，期望 ErrTokenExpired", err)
	}
}

func TestParseTokenForRefreshAllowsGraceWindow(t *testing.T) {
	mgr := NewJWTManager("jwt-secret", 1)
	expired := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		UserID: 3,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
			Issuer:    "airgate",
		},
	})
	token, err := expired.SignedString(mgr.secret)
	if err != nil {
		t.Fatalf("签发过期 token 失败: %v", err)
	}

	claims, err := mgr.ParseTokenForRefresh(token, 2*time.Minute)
	if err != nil {
		t.Fatalf("宽限期内解析失败: %v", err)
	}
	if claims.UserID != 3 {
		t.Fatalf("UserID = %d，期望 3", claims.UserID)
	}
}
