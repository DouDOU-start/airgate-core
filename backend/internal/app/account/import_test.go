package account

import (
	"context"
	"testing"
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

type importCaptureRepo struct {
	stubAccountRepo
	input PersistCreateInput
}

func (r *importCaptureRepo) Create(_ context.Context, input PersistCreateInput) (Account, error) {
	r.input = input
	return Account{
		ID:       1,
		Name:     input.Name,
		Platform: input.Platform,
		Type:     input.Type,
	}, nil
}
