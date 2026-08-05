package account

import (
	"context"
	"errors"
	"testing"
)

func TestListByRuntimeStat_SortsConcurrencyDesc(t *testing.T) {
	repo := &stubAccountRepo{
		ids: []int{1, 2, 3},
		byID: map[int]Account{
			1: {ID: 1, Name: "a", Platform: "codex"},
			2: {ID: 2, Name: "b", Platform: "codex"},
			3: {ID: 3, Name: "c", Platform: "codex"},
		},
	}
	svc := NewService(repo, "0123456789abcdef0123456789abcdef")
	svc.SetRuntimeStatsReaders(
		stubAccountConcurrency{counts: map[int]int{1: 1, 2: 5, 3: 3}},
		nil,
	)

	result, err := svc.List(context.Background(), ListFilter{
		Page: 1, PageSize: 10, SortBy: SortByConcurrency, SortOrder: "desc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.List) != 3 {
		t.Fatalf("len=%d", len(result.List))
	}
	// desc: 2(5), 3(3), 1(1)
	want := []int{2, 3, 1}
	for i, id := range want {
		if result.List[i].ID != id {
			t.Fatalf("pos %d: got %d want %d", i, result.List[i].ID, id)
		}
		if result.List[i].CurrentConcurrency != map[int]int{2: 5, 3: 3, 1: 1}[id] {
			t.Fatalf("metrics not attached for id %d", id)
		}
	}
}

func TestListByRuntimeStat_TooManyCandidates(t *testing.T) {
	ids := make([]int, maxRuntimeStatSortCandidates+1)
	for i := range ids {
		ids[i] = i + 1
	}
	repo := &stubAccountRepo{ids: ids, byID: map[int]Account{}}
	svc := NewService(repo, "0123456789abcdef0123456789abcdef")
	_, err := svc.List(context.Background(), ListFilter{
		Page: 1, PageSize: 20, SortBy: SortByRPM,
	})
	if !errors.Is(err, ErrTooManySortCandidates) {
		t.Fatalf("expected ErrTooManySortCandidates, got %v", err)
	}
}

type stubAccountRepo struct {
	ids  []int
	byID map[int]Account
}

func (s *stubAccountRepo) List(context.Context, ListFilter) ([]Account, int64, error) {
	return nil, 0, errors.New("not used")
}
func (s *stubAccountRepo) ListAll(context.Context, ListFilter) ([]Account, error) {
	return nil, errors.New("not used")
}
func (s *stubAccountRepo) ListIDs(context.Context, ListFilter) ([]int, error) {
	return append([]int(nil), s.ids...), nil
}
func (s *stubAccountRepo) ListByIDs(_ context.Context, ids []int) ([]Account, error) {
	out := make([]Account, 0, len(ids))
	for _, id := range ids {
		if a, ok := s.byID[id]; ok {
			out = append(out, a)
		}
	}
	// 故意打乱返回顺序，验证 reorder
	if len(out) > 1 {
		out[0], out[len(out)-1] = out[len(out)-1], out[0]
	}
	return out, nil
}
func (s *stubAccountRepo) FindByID(context.Context, int, LoadOptions) (Account, error) {
	return Account{}, errors.New("not used")
}
func (s *stubAccountRepo) Create(context.Context, PersistCreateInput) (Account, error) {
	return Account{}, errors.New("not used")
}
func (s *stubAccountRepo) Update(context.Context, int, PersistUpdateInput) (Account, error) {
	return Account{}, errors.New("not used")
}
func (s *stubAccountRepo) Delete(context.Context, int) error { return errors.New("not used") }
func (s *stubAccountRepo) SaveCredentials(context.Context, int, string, string) error {
	return errors.New("not used")
}

type stubAccountConcurrency struct {
	counts map[int]int
}

func (s stubAccountConcurrency) GetAccountCurrentCounts(_ context.Context, ids []int) map[int]int {
	out := make(map[int]int, len(ids))
	for _, id := range ids {
		out[id] = s.counts[id]
	}
	return out
}
