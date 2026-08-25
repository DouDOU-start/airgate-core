package auth

import (
	"strings"
	"testing"
)

func TestGenerateAPIKeyPrefixesAndHashes(t *testing.T) {
	t.Parallel()

	key, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey error: %v", err)
	}
	if !strings.HasPrefix(key, apiKeyPrefix) {
		t.Fatalf("API key prefix = %q, want %q", key[:len(apiKeyPrefix)], apiKeyPrefix)
	}
	if hash != HashAPIKey(key) {
		t.Fatalf("hash = %q, want HashAPIKey(key)", hash)
	}

	adminKey, adminHash, err := GenerateAdminAPIKey()
	if err != nil {
		t.Fatalf("GenerateAdminAPIKey error: %v", err)
	}
	if !strings.HasPrefix(adminKey, adminKeyPrefix) {
		t.Fatalf("admin key prefix = %q, want %q", adminKey[:len(adminKeyPrefix)], adminKeyPrefix)
	}
	if adminHash != HashAPIKey(adminKey) {
		t.Fatalf("admin hash = %q, want HashAPIKey(adminKey)", adminHash)
	}
}
