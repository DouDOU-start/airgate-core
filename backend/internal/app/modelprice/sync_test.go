package modelprice

import (
	"context"
	"math"
	"testing"
)

type staticSyncFetcher []byte

func (f staticSyncFetcher) Fetch(context.Context, string) ([]byte, error) { return f, nil }

type syncRepo struct {
	items   []ModelPrice
	updated UpdateInput
	created []CreateInput
}

func (r *syncRepo) List(context.Context, ListFilter) ([]ModelPrice, int64, error) { return nil, 0, nil }
func (r *syncRepo) ListAll(context.Context) ([]ModelPrice, error)                 { return r.items, nil }
func (r *syncRepo) FindByID(context.Context, int) (ModelPrice, error)             { return ModelPrice{}, nil }
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
func (r *syncRepo) Update(_ context.Context, _ int, input UpdateInput) (ModelPrice, error) {
	r.updated = input
	return ModelPrice{}, nil
}
func (r *syncRepo) Delete(context.Context, int) error                   { return nil }
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

func TestEmbeddedCatalogIsValid(t *testing.T) {
	service := NewService(&syncRepo{})
	remote, err := service.fetchRemotePrices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(remote) == 0 {
		t.Fatal("仓库内置模型价格目录为空")
	}
	if _, ok := remote["gpt-5.4"]; !ok {
		t.Fatal("仓库内置模型价格目录缺少 gpt-5.4")
	}
}
