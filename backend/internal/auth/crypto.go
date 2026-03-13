package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
)

// deriveAESKey 从 hex 编码的 secret 中取前 32 字节作为 AES-256 密钥
func deriveAESKey(secret string) ([]byte, error) {
	raw, err := hex.DecodeString(secret)
	if err != nil {
		return nil, fmt.Errorf("secret 不是有效的 hex 字符串: %w", err)
	}
	if len(raw) < 32 {
		return nil, fmt.Errorf("secret 长度不足 32 字节")
	}
	return raw[:32], nil
}

// EncryptAPIKey 使用 AES-GCM 加密 API 密钥，返回 base64 编码的密文
func EncryptAPIKey(plainKey, secret string) (string, error) {
	key, err := deriveAESKey(secret)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("创建 AES cipher 失败: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("创建 GCM 失败: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("生成 nonce 失败: %w", err)
	}

	// nonce + 密文拼接后 base64 编码
	ciphertext := gcm.Seal(nonce, nonce, []byte(plainKey), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptAPIKey 使用 AES-GCM 解密 base64 编码的密文，返回明文
func DecryptAPIKey(encrypted, secret string) (string, error) {
	key, err := deriveAESKey(secret)
	if err != nil {
		return "", err
	}

	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", fmt.Errorf("base64 解码失败: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("创建 AES cipher 失败: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("创建 GCM 失败: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("密文数据太短")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("解密失败: %w", err)
	}

	return string(plaintext), nil
}
