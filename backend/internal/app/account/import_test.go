package account

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestImportClearsLocalBindings(t *testing.T) {
	proxyID := int64(11)
	repo := &importCaptureRepo{}
	service := NewService(repo, "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")

	result := service.Import(context.Background(), []CreateInput{{
		Name:        "测试账号",
		Platform:    "xai",
		Type:        TypeAPIKey,
		Credentials: map[string]string{"api_key": "test-key"},
		GroupIDs:    []int64{3, 7},
		ProxyID:     &proxyID,
	}})

	if result.Imported != 1 || result.Failed != 0 {
		t.Fatalf("导入结果不符合预期：%+v", result)
	}
	if len(repo.input.GroupIDs) != 0 {
		t.Fatalf("服务层导入不应绑定旧分组：%v", repo.input.GroupIDs)
	}
	if repo.input.ProxyID != nil {
		t.Fatalf("服务层导入不应绑定旧代理：%v", *repo.input.ProxyID)
	}
}

func TestImportRefreshesOAuthUsage(t *testing.T) {
	repo := &importCaptureRepo{}
	service := NewService(repo, "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	fetched := false
	service.usageFetcher = func(_ context.Context, platform, accountType string, credentials map[string]string, _ string) (UsageSnapshot, error) {
		fetched = true
		if platform != "codex" || accountType != TypeOAuth || credentials["access_token"] != "测试令牌" {
			t.Fatalf("首次用量查询参数错误：platform=%s type=%s credentials=%v", platform, accountType, credentials)
		}
		return UsageSnapshot{
			CapturedAt: time.Date(2026, 8, 5, 8, 0, 0, 0, time.UTC),
			PlanType:   "plus",
			Windows:    []UsageWindow{{Key: "weekly", UsedPercent: 25}},
		}, nil
	}

	result := service.Import(context.Background(), []CreateInput{{
		Name:        "Codex OAuth",
		Platform:    "codex",
		Type:        TypeOAuth,
		Credentials: map[string]string{"access_token": "测试令牌"},
	}})

	if result.Imported != 1 || result.Failed != 0 || !fetched {
		t.Fatalf("OAuth 导入后应立即刷新用量：%+v fetched=%v", result, fetched)
	}
	usage := UsageFromExtra(repo.updated.Extra)
	if usage == nil || len(usage.Windows) != 1 || usage.Windows[0].UsedPercent != 25 {
		t.Fatalf("首次用量未写入账号：%+v", usage)
	}
}

func TestImportKeepsAccountWhenInitialUsageRefreshFails(t *testing.T) {
	repo := &importCaptureRepo{}
	service := NewService(repo, "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	service.usageFetcher = func(context.Context, string, string, map[string]string, string) (UsageSnapshot, error) {
		return UsageSnapshot{}, errors.New("上游用量接口暂时不可用")
	}

	result := service.Import(context.Background(), []CreateInput{{
		Name:        "Grok OAuth",
		Platform:    "xai",
		Type:        TypeOAuth,
		Credentials: map[string]string{"access_token": "测试令牌"},
	}})

	if result.Imported != 1 || result.Failed != 0 {
		t.Fatalf("首次用量查询失败不应回滚账号：%+v", result)
	}
	if repo.updateCalls != 0 {
		t.Fatalf("用量查询失败时不应写入空快照，更新次数：%d", repo.updateCalls)
	}
}

func TestCreateFromCodexImportReturnsInitialUsage(t *testing.T) {
	repo := &importCaptureRepo{}
	service := NewService(repo, "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	service.usageFetcher = func(context.Context, string, string, map[string]string, string) (UsageSnapshot, error) {
		return UsageSnapshot{
			CapturedAt: time.Date(2026, 8, 5, 8, 0, 0, 0, time.UTC),
			Windows:    []UsageWindow{{Key: "5h", UsedPercent: 10}},
		}, nil
	}

	item, err := service.createFromCodexImport(
		context.Background(),
		OAuthStartInput{Name: "Codex Session"},
		map[string]string{"access_token": "测试令牌"},
		"",
	)
	if err != nil {
		t.Fatalf("Codex 专用导入失败：%v", err)
	}
	if item.Usage == nil || len(item.Usage.Windows) != 1 || item.Usage.Windows[0].UsedPercent != 10 {
		t.Fatalf("Codex 专用导入应直接返回首次用量：%+v", item.Usage)
	}
}

type importCaptureRepo struct {
	stubAccountRepo
	input       PersistCreateInput
	created     Account
	updated     Account
	updateCalls int
}

func (r *importCaptureRepo) Create(_ context.Context, input PersistCreateInput) (Account, error) {
	r.input = input
	r.created = Account{
		ID:             1,
		Name:           input.Name,
		Platform:       input.Platform,
		Type:           input.Type,
		CredentialsEnc: input.CredentialsEnc,
		Extra:          input.Extra,
	}
	return r.created, nil
}

func (r *importCaptureRepo) FindByID(context.Context, int, LoadOptions) (Account, error) {
	return r.created, nil
}

func (r *importCaptureRepo) Update(_ context.Context, _ int, input PersistUpdateInput) (Account, error) {
	r.updateCalls++
	r.updated = r.created
	r.updated.Extra = input.Extra
	return r.updated, nil
}
