package bootstrap

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/auth"
)

const migrationTestSecret = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func TestPrepareLegacyAccountCredentialEncryptsJSON(t *testing.T) {
	item := legacyAccountCredential{
		id:  7,
		raw: `{"access_token":"token","email":"user@example.com"}`,
	}

	encrypted, email, shouldMigrate, err := prepareLegacyAccountCredential(item, migrationTestSecret)
	if err != nil {
		t.Fatalf("准备旧凭证失败: %v", err)
	}
	if !shouldMigrate || email != "user@example.com" || encrypted == "" {
		t.Fatalf("迁移结果不完整: migrate=%v email=%q encrypted=%q", shouldMigrate, email, encrypted)
	}
	plain, err := auth.DecryptAPIKey(encrypted, migrationTestSecret)
	if err != nil {
		t.Fatalf("解密迁移结果失败: %v", err)
	}
	var credentials map[string]string
	if err := json.Unmarshal([]byte(plain), &credentials); err != nil {
		t.Fatalf("迁移结果不是合法 JSON: %v", err)
	}
	if credentials["access_token"] != "token" {
		t.Fatalf("access_token 未保留: %v", credentials)
	}
}

func TestPrepareLegacyAccountCredentialKeepsExistingCiphertext(t *testing.T) {
	existing, err := auth.EncryptAPIKey(`{"access_token":"token"}`, migrationTestSecret)
	if err != nil {
		t.Fatalf("准备密文失败: %v", err)
	}
	item := legacyAccountCredential{id: 8, raw: `{"access_token":"token"}`, encrypted: existing}

	encrypted, _, shouldMigrate, err := prepareLegacyAccountCredential(item, migrationTestSecret)
	if err != nil || !shouldMigrate || encrypted != existing {
		t.Fatalf("已有密文被改写: migrate=%v encrypted=%q err=%v", shouldMigrate, encrypted, err)
	}
}

func TestPrepareLegacyAccountCredentialValidatesCiphertextAfterLegacyValueCleared(t *testing.T) {
	existing, err := auth.EncryptAPIKey(`{"access_token":"token"}`, migrationTestSecret)
	if err != nil {
		t.Fatalf("准备密文失败: %v", err)
	}
	item := legacyAccountCredential{id: 8, raw: `{}`, encrypted: existing}

	if _, _, shouldMigrate, err := prepareLegacyAccountCredential(item, strings.Repeat("11", 32)); err == nil || shouldMigrate {
		t.Fatalf("错误密钥应在旧字段清空后仍被识别: migrate=%v err=%v", shouldMigrate, err)
	}
}

func TestPrepareLegacyAccountCredentialRejectsConflictingValues(t *testing.T) {
	existing, err := auth.EncryptAPIKey(`{"access_token":"new-token"}`, migrationTestSecret)
	if err != nil {
		t.Fatalf("准备密文失败: %v", err)
	}
	item := legacyAccountCredential{
		id:        8,
		raw:       `{"access_token":"old-token"}`,
		encrypted: existing,
	}

	if _, _, _, err := prepareLegacyAccountCredential(item, migrationTestSecret); err == nil {
		t.Fatal("明文与密文冲突时应阻断迁移")
	}
}

func TestPrepareLegacyAccountCredentialRejectsInvalidJSON(t *testing.T) {
	item := legacyAccountCredential{id: 9, raw: "not-json"}
	if _, _, _, err := prepareLegacyAccountCredential(item, migrationTestSecret); err == nil {
		t.Fatal("非法旧凭证应阻断迁移")
	}
}

func TestEmptyLegacyCredentialSQL(t *testing.T) {
	for _, dataType := range []string{"json", "jsonb", "text", "character varying"} {
		if _, err := emptyLegacyCredentialSQL(dataType); err != nil {
			t.Fatalf("类型 %s 应受支持: %v", dataType, err)
		}
	}
	if _, err := emptyLegacyCredentialSQL("bytea"); err == nil {
		t.Fatal("未知旧字段类型应拒绝迁移")
	}
}

func TestSharedCredentialEndpointsToSplit(t *testing.T) {
	items := []endpointCredential{
		{keyID: 1, credentialID: 10},
		{keyID: 2, credentialID: 10},
		{keyID: 3, credentialID: 10},
		{keyID: 4, credentialID: 20},
		{keyID: 5, credentialID: 20},
		{keyID: 6, credentialID: 30},
	}
	want := []endpointCredential{
		{keyID: 2, credentialID: 10},
		{keyID: 3, credentialID: 10},
		{keyID: 5, credentialID: 20},
	}
	if got := sharedCredentialEndpointsToSplit(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("待拆分端点 = %+v，期望 %+v", got, want)
	}
}

func TestSharedCredentialEndpointsToSplitIsIdempotentForSingleProtocolData(t *testing.T) {
	items := []endpointCredential{
		{keyID: 1, credentialID: 10},
		{keyID: 2, credentialID: 20},
		{keyID: 3, credentialID: 30},
	}
	if got := sharedCredentialEndpointsToSplit(items); len(got) != 0 {
		t.Fatalf("单协议凭证仍被判定为待拆分: %+v", got)
	}
}
