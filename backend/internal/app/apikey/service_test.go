package apikey

import "testing"

func TestDisplayKeyPrefixPrefersHint(t *testing.T) {
	prefix := DisplayKeyPrefix(Key{
		KeyHint: "sk-abcd...wxyz",
		KeyHash: "1234567890abcdef",
	})
	if prefix != "sk-abcd...wxyz" {
		t.Fatalf("expected hint to be used, got %q", prefix)
	}
}

func TestParseExpiresAtRejectsInvalidFormat(t *testing.T) {
	value := "2026/04/02"
	_, _, err := parseExpiresAt(&value)
	if err != ErrInvalidExpiresAt {
		t.Fatalf("expected ErrInvalidExpiresAt, got %v", err)
	}
}
