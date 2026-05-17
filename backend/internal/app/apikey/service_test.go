package apikey

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	corauth "github.com/DouDOU-start/airgate-core/internal/auth"
)

const testAPIKeySecret = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

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

func TestParseExpiresAtClearsWhenEmpty(t *testing.T) {
	value := ""
	expiresAt, hasExpiresAt, err := parseExpiresAt(&value)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !hasExpiresAt {
		t.Fatal("expected expires_at to be marked for update")
	}
	if expiresAt != nil {
		t.Fatalf("expected nil expires_at, got %v", expiresAt)
	}
}

func TestListByUserNormalizesPaginationAndAttachesUsage(t *testing.T) {
	var capturedFilter ListFilter
	var capturedIDs []int
	service := NewService(apiKeyStubRepository{
		listByUser: func(_ context.Context, userID int, filter ListFilter) ([]Key, int64, error) {
			if userID != 7 {
				t.Fatalf("用户 ID = %d，期望 7", userID)
			}
			capturedFilter = filter
			return []Key{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}}, 2, nil
		},
		keyUsage: func(_ context.Context, keyIDs []int, todayStart time.Time) (map[int]float64, map[int]float64, error) {
			capturedIDs = append([]int(nil), keyIDs...)
			if todayStart.IsZero() {
				t.Fatal("今日起点不应为空")
			}
			return map[int]float64{1: 1.2}, map[int]float64{2: 9.8}, nil
		},
	}, testAPIKeySecret)

	result, err := service.ListByUser(t.Context(), 7, ListFilter{}, "Asia/Shanghai")
	if err != nil {
		t.Fatalf("查询 API Key 失败: %v", err)
	}
	if capturedFilter.Page != 1 || capturedFilter.PageSize != 20 {
		t.Fatalf("分页未归一化: %+v", capturedFilter)
	}
	if !reflect.DeepEqual(capturedIDs, []int{1, 2}) {
		t.Fatalf("用量查询 keyIDs = %v，期望 [1 2]", capturedIDs)
	}
	if result.Total != 2 || result.List[0].TodayCost != 1.2 || result.List[1].ThirtyDayCost != 9.8 {
		t.Fatalf("列表结果异常: %+v", result)
	}
}

func TestListAdminNormalizesPaginationWithoutUsageAggregation(t *testing.T) {
	var capturedFilter ListFilter
	var usageCalled bool
	service := NewService(apiKeyStubRepository{
		listAdmin: func(_ context.Context, filter ListFilter) ([]Key, int64, error) {
			capturedFilter = filter
			return []Key{{ID: 3, Name: "admin-key"}}, 1, nil
		},
		keyUsage: func(_ context.Context, _ []int, _ time.Time) (map[int]float64, map[int]float64, error) {
			usageCalled = true
			return nil, nil, nil
		},
	}, testAPIKeySecret)

	result, err := service.ListAdmin(t.Context(), ListFilter{Keyword: "prod"})
	if err != nil {
		t.Fatalf("管理员查询 API Key 失败: %v", err)
	}
	if capturedFilter.Page != 1 || capturedFilter.PageSize != 20 || capturedFilter.Keyword != "prod" {
		t.Fatalf("分页或关键词未归一化: %+v", capturedFilter)
	}
	if usageCalled {
		t.Fatal("管理员搜索列表不应触发用量聚合")
	}
	if result.Total != 1 || len(result.List) != 1 || result.List[0].ID != 3 {
		t.Fatalf("列表结果异常: %+v", result)
	}
}

func TestCreateOwnedBuildsMutationAndReturnsPlainKey(t *testing.T) {
	expiresAt := "2026-05-15T10:00:00Z"
	var captured Mutation
	service := NewService(apiKeyStubRepository{
		groupAccess: func(_ context.Context, userID, groupID int) (GroupAccess, error) {
			if userID != 7 || groupID != 3 {
				t.Fatalf("分组访问参数异常: user=%d group=%d", userID, groupID)
			}
			return GroupAccess{Exists: true, Allowed: true}, nil
		},
		create: func(_ context.Context, mutation Mutation) (Key, error) {
			captured = mutation
			return Key{ID: 10, Name: derefString(mutation.Name), UserID: derefInt(mutation.UserID)}, nil
		},
	}, testAPIKeySecret)

	item, err := service.CreateOwned(t.Context(), 7, CreateInput{
		Name:           "生产 Key",
		GroupID:        3,
		IPWhitelist:    []string{"127.0.0.1"},
		QuotaUSD:       99,
		SellRate:       1.2,
		MaxConcurrency: -1,
		ExpiresAt:      &expiresAt,
	})
	if err != nil {
		t.Fatalf("创建 API Key 失败: %v", err)
	}
	if item.ID != 10 || item.PlainKey == "" {
		t.Fatalf("创建结果异常: %+v", item)
	}
	if derefString(captured.Name) != "生产 Key" || derefInt(captured.UserID) != 7 || derefInt(captured.GroupID) != 3 {
		t.Fatalf("基础 mutation 异常: %+v", captured)
	}
	if !captured.HasIPWhitelist || len(captured.IPWhitelist) != 1 {
		t.Fatalf("IP 白名单 mutation 异常: %+v", captured)
	}
	if derefInt(captured.MaxConcurrency) != 0 {
		t.Fatalf("负并发上限应归零，得到 %+v", captured.MaxConcurrency)
	}
	if captured.KeyHint == nil || captured.KeyHash == nil || captured.KeyEncrypted == nil || captured.ExpiresAt == nil || !captured.HasExpiresAt {
		t.Fatalf("密钥或过期时间 mutation 缺失: %+v", captured)
	}
	plain, err := corauth.DecryptAPIKey(*captured.KeyEncrypted, testAPIKeySecret)
	if err != nil {
		t.Fatalf("创建密文无法解密: %v", err)
	}
	if plain != item.PlainKey {
		t.Fatalf("密文明文 = %q，期望返回的 PlainKey", plain)
	}
}

func TestCreateOwnedRejectsUnavailableGroup(t *testing.T) {
	service := NewService(apiKeyStubRepository{
		groupAccess: func(context.Context, int, int) (GroupAccess, error) {
			return GroupAccess{Exists: true, Allowed: false}, nil
		},
	}, testAPIKeySecret)

	_, err := service.CreateOwned(t.Context(), 7, CreateInput{GroupID: 3})
	if !errors.Is(err, ErrGroupForbidden) {
		t.Fatalf("错误 = %v，期望 ErrGroupForbidden", err)
	}
}

func TestRevealOwnedDecryptsKeyAndRejectsLegacyKey(t *testing.T) {
	encrypted, err := corauth.EncryptAPIKey("sk-secret", testAPIKeySecret)
	if err != nil {
		t.Fatalf("准备密文失败: %v", err)
	}
	service := NewService(apiKeyStubRepository{
		findOwned: func(_ context.Context, _, id int) (Key, error) {
			if id == 1 {
				return Key{ID: id, KeyEncrypted: encrypted}, nil
			}
			return Key{ID: id}, nil
		},
	}, testAPIKeySecret)

	item, err := service.RevealOwned(t.Context(), 7, 1)
	if err != nil {
		t.Fatalf("查看明文失败: %v", err)
	}
	if item.PlainKey != "sk-secret" {
		t.Fatalf("明文 = %q，期望 sk-secret", item.PlainKey)
	}
	if _, err := service.RevealOwned(t.Context(), 7, 2); !errors.Is(err, ErrLegacyKeyNotReveal) {
		t.Fatalf("遗留 key 错误 = %v，期望 ErrLegacyKeyNotReveal", err)
	}
}

func TestUpdateOwnedBuildsMutationAndChecksGroup(t *testing.T) {
	name := "更新后的 Key"
	groupID := int64(8)
	clearExpiresAt := ""
	status := "disabled"
	var captured Mutation
	service := NewService(apiKeyStubRepository{
		groupAccess: func(_ context.Context, userID, groupID int) (GroupAccess, error) {
			if userID != 7 || groupID != 8 {
				t.Fatalf("分组访问参数异常: user=%d group=%d", userID, groupID)
			}
			return GroupAccess{Exists: true, Allowed: true}, nil
		},
		updateOwned: func(_ context.Context, userID, id int, mutation Mutation) (Key, error) {
			if userID != 7 || id != 11 {
				t.Fatalf("更新参数异常: user=%d id=%d", userID, id)
			}
			captured = mutation
			return Key{ID: id, Name: derefString(mutation.Name)}, nil
		},
	}, testAPIKeySecret)

	item, err := service.UpdateOwned(t.Context(), 7, 11, UpdateInput{
		Name:           &name,
		GroupID:        &groupID,
		IPBlacklist:    []string{"10.0.0.1"},
		HasIPBlacklist: true,
		ExpiresAt:      &clearExpiresAt,
		Status:         &status,
	})
	if err != nil {
		t.Fatalf("更新 API Key 失败: %v", err)
	}
	if item.ID != 11 || item.Name != name {
		t.Fatalf("更新结果异常: %+v", item)
	}
	if derefString(captured.Name) != name || derefInt(captured.GroupID) != 8 || derefString(captured.Status) != "disabled" {
		t.Fatalf("mutation 字段异常: %+v", captured)
	}
	if !captured.HasIPBlacklist || len(captured.IPBlacklist) != 1 || !captured.HasExpiresAt || captured.ExpiresAt != nil {
		t.Fatalf("列表或过期时间 mutation 异常: %+v", captured)
	}
}

func TestUpdateAdminDoesNotCheckGroupAccess(t *testing.T) {
	groupID := int64(9)
	var checkedGroup bool
	service := NewService(apiKeyStubRepository{
		groupAccess: func(context.Context, int, int) (GroupAccess, error) {
			checkedGroup = true
			return GroupAccess{}, nil
		},
		updateAdmin: func(_ context.Context, id int, mutation Mutation) (Key, error) {
			return Key{ID: id, UserID: 42, GroupID: mutation.GroupID}, nil
		},
	}, testAPIKeySecret)

	item, err := service.UpdateAdmin(t.Context(), 13, UpdateInput{GroupID: &groupID})
	if err != nil {
		t.Fatalf("管理员更新失败: %v", err)
	}
	if checkedGroup {
		t.Fatal("管理员更新不应检查用户分组权限")
	}
	if item.GroupID == nil || *item.GroupID != 9 {
		t.Fatalf("更新分组异常: %+v", item.GroupID)
	}
}

type apiKeyStubRepository struct {
	listByUser  func(context.Context, int, ListFilter) ([]Key, int64, error)
	listAdmin   func(context.Context, ListFilter) ([]Key, int64, error)
	keyUsage    func(context.Context, []int, time.Time) (map[int]float64, map[int]float64, error)
	groupAccess func(context.Context, int, int) (GroupAccess, error)
	create      func(context.Context, Mutation) (Key, error)
	updateOwned func(context.Context, int, int, Mutation) (Key, error)
	updateAdmin func(context.Context, int, Mutation) (Key, error)
	deleteOwned func(context.Context, int, int) error
	findOwned   func(context.Context, int, int) (Key, error)
}

func (s apiKeyStubRepository) ListByUser(ctx context.Context, userID int, filter ListFilter) ([]Key, int64, error) {
	if s.listByUser == nil {
		return nil, 0, nil
	}
	return s.listByUser(ctx, userID, filter)
}

func (s apiKeyStubRepository) ListAdmin(ctx context.Context, filter ListFilter) ([]Key, int64, error) {
	if s.listAdmin == nil {
		return nil, 0, nil
	}
	return s.listAdmin(ctx, filter)
}

func (s apiKeyStubRepository) KeyUsage(ctx context.Context, keyIDs []int, todayStart time.Time) (map[int]float64, map[int]float64, error) {
	if s.keyUsage == nil {
		return map[int]float64{}, map[int]float64{}, nil
	}
	return s.keyUsage(ctx, keyIDs, todayStart)
}

func (s apiKeyStubRepository) GetGroupAccess(ctx context.Context, userID, groupID int) (GroupAccess, error) {
	if s.groupAccess == nil {
		return GroupAccess{Exists: true, Allowed: true}, nil
	}
	return s.groupAccess(ctx, userID, groupID)
}

func (s apiKeyStubRepository) Create(ctx context.Context, mutation Mutation) (Key, error) {
	if s.create == nil {
		return Key{}, nil
	}
	return s.create(ctx, mutation)
}

func (s apiKeyStubRepository) UpdateOwned(ctx context.Context, userID, id int, mutation Mutation) (Key, error) {
	if s.updateOwned == nil {
		return Key{}, nil
	}
	return s.updateOwned(ctx, userID, id, mutation)
}

func (s apiKeyStubRepository) UpdateAdmin(ctx context.Context, id int, mutation Mutation) (Key, error) {
	if s.updateAdmin == nil {
		return Key{}, nil
	}
	return s.updateAdmin(ctx, id, mutation)
}

func (s apiKeyStubRepository) DeleteOwned(ctx context.Context, userID, id int) error {
	if s.deleteOwned == nil {
		return nil
	}
	return s.deleteOwned(ctx, userID, id)
}

func (s apiKeyStubRepository) FindOwned(ctx context.Context, userID, id int) (Key, error) {
	if s.findOwned == nil {
		return Key{}, nil
	}
	return s.findOwned(ctx, userID, id)
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func derefInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
