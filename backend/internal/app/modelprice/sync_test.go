package modelprice

import (
	"context"
	_ "embed"
	"errors"
	"math"
	"testing"
)

//go:embed model_prices_and_context_window.json
var repositoryModelPriceCatalog []byte

type staticSyncFetcher []byte

func (f staticSyncFetcher) Fetch(context.Context, string) ([]byte, error) { return f, nil }

type syncRepo struct {
	items      []ModelPrice
	updated    UpdateInput
	updatedIDs []int
	deletedIDs []int
	failIDs    map[int]error
	created    []CreateInput
	listed     ListFilter
}

func (r *syncRepo) List(_ context.Context, filter ListFilter) ([]ModelPrice, int64, error) {
	r.listed = filter
	return r.items, int64(len(r.items)), nil
}
func (r *syncRepo) ListAll(context.Context) ([]ModelPrice, error)     { return r.items, nil }
func (r *syncRepo) FindByID(context.Context, int) (ModelPrice, error) { return ModelPrice{}, nil }
func (r *syncRepo) Create(_ context.Context, input CreateInput) (ModelPrice, error) {
	r.created = append(r.created, input)
	return ModelPrice{ID: len(r.items) + 1, Model: input.Model}, nil
}

func TestSyncCandidatesAndSelectedImport(t *testing.T) {
	repo := &syncRepo{items: []ModelPrice{{ID: 1, Model: "existing-model"}}}
	service := NewService(repo)
	service.SetSyncFetcher(staticSyncFetcher(`{
		"existing-model": {"input_cost_per_token": 0.000001, "litellm_provider": "local"},
		"openai/new-model": {"input_cost_per_token": 0.000003, "output_cost_per_token": 0.000009, "litellm_provider": "openai", "mode": "chat"},
		"openai/not-selected": {"input_cost_per_token": 0.000004}
	}`))

	candidates, err := service.SyncCandidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var foundNew, foundExisting bool
	for _, candidate := range candidates {
		switch candidate.Model {
		case "new-model":
			foundNew = !candidate.Exists && candidate.Provider == "openai" && candidate.InputPrice == 3
		case "existing-model":
			foundExisting = candidate.Exists
		}
	}
	if !foundNew || !foundExisting {
		t.Fatalf("candidates = %+v", candidates)
	}

	result, err := service.SyncSelected(context.Background(), []string{"new-model"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 1 || len(repo.created) != 1 || repo.created[0].Model != "new-model" {
		t.Fatalf("result = %+v, created = %+v", result, repo.created)
	}
}
func (r *syncRepo) Update(_ context.Context, id int, input UpdateInput) (ModelPrice, error) {
	if err := r.failIDs[id]; err != nil {
		return ModelPrice{}, err
	}
	r.updated = input
	r.updatedIDs = append(r.updatedIDs, id)
	return ModelPrice{}, nil
}
func (r *syncRepo) Delete(_ context.Context, id int) error {
	if err := r.failIDs[id]; err != nil {
		return err
	}
	r.deletedIDs = append(r.deletedIDs, id)
	return nil
}
func (r *syncRepo) ListTags(context.Context) ([]Tag, error)             { return nil, nil }
func (r *syncRepo) CreateTag(context.Context, string) (Tag, error)      { return Tag{}, nil }
func (r *syncRepo) RenameTag(context.Context, int, string) (Tag, error) { return Tag{}, nil }
func (r *syncRepo) DeleteTag(context.Context, int) error                { return nil }

func TestSyncConvertsPerTokenPricesToPerMillion(t *testing.T) {
	repo := &syncRepo{items: []ModelPrice{{ID: 9, Model: "custom-model"}}}
	service := NewService(repo)
	service.SetSyncFetcher(staticSyncFetcher(`{
		"openai/custom-model": {
			"input_cost_per_token": 0.000002,
			"output_cost_per_token": 0.000008,
			"cache_read_input_token_cost": 0.0000002
		}
	}`))
	result, err := service.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated != 1 || result.Matched < 1 {
		t.Fatalf("result = %+v", result)
	}
	if repo.updated.InputPrice == nil || *repo.updated.InputPrice != 2 ||
		repo.updated.OutputPrice == nil || *repo.updated.OutputPrice != 8 ||
		repo.updated.CachedInputPrice == nil || math.Abs(*repo.updated.CachedInputPrice-0.2) > 1e-9 {
		t.Fatalf("update = %+v", repo.updated)
	}
}

func TestRepositoryCatalogIsValid(t *testing.T) {
	service := NewService(&syncRepo{})
	service.SetSyncFetcher(staticSyncFetcher(repositoryModelPriceCatalog))
	remote, err := service.fetchRemotePrices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(remote) == 0 {
		t.Fatal("仓库模型价格目录为空")
	}
	if _, ok := remote["gpt-5.4"]; !ok {
		t.Fatal("仓库模型价格目录缺少 gpt-5.4")
	}
}

func TestBulkUpdateAllowsPartialSuccessAndDeduplicatesIDs(t *testing.T) {
	repo := &syncRepo{failIDs: map[int]error{2: errors.New("写入失败")}}
	service := NewService(repo)
	result := service.BulkUpdate(context.Background(), BulkUpdateInput{
		IDs: []int{1, 1, 2, 3}, Action: BulkActionDisable,
	})
	if result.Success != 2 || result.Failed != 1 {
		t.Fatalf("result = %+v", result)
	}
	if len(repo.updatedIDs) != 2 || repo.updatedIDs[0] != 1 || repo.updatedIDs[1] != 3 {
		t.Fatalf("updated IDs = %v", repo.updatedIDs)
	}
	if repo.updated.Enabled == nil || *repo.updated.Enabled {
		t.Fatalf("update = %+v", repo.updated)
	}
}

func TestDisabledModelsAreExcludedFromPricingAndPublicList(t *testing.T) {
	repo := &syncRepo{items: []ModelPrice{
		{ID: 1, Model: "enabled-model", Enabled: true, MarketVisible: true},
		{ID: 2, Model: "disabled-model", Enabled: false, MarketVisible: true},
	}}
	service := NewService(repo)

	prices, err := service.LoadAllPrices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := prices["enabled-model"]; !ok {
		t.Fatal("启用模型应进入计费目录")
	}
	if _, ok := prices["disabled-model"]; ok {
		t.Fatal("停用模型不应进入计费目录")
	}

	if _, err := service.ListPublic(context.Background(), ListFilter{}); err != nil {
		t.Fatal(err)
	}
	if !repo.listed.EnabledOnly || !repo.listed.MarketVisibleOnly {
		t.Fatalf("公开列表过滤条件 = %+v", repo.listed)
	}
}
