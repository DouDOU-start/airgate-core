package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
)

func TestRouteAccountsAnyModelBypassesOnlyModelRouting(t *testing.T) {
	t.Parallel()

	accounts := []*ent.Account{{ID: 1}, {ID: 2}}
	s := &Scheduler{routeCache: newRouteCache(time.Minute)}
	s.routeCache.Set(7, "openai", accounts, map[string][]int64{"gpt-5.4": {2}})

	ordinary, err := s.routeAccounts(context.Background(), "openai", "", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordinary) != 0 {
		t.Fatalf("ordinary empty-model routing = %#v, want no accounts", ordinary)
	}

	unknownModel, err := s.routeAccountsForSelection(context.Background(), "openai", "", 7, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(unknownModel) != 2 || unknownModel[0].ID != 1 || unknownModel[1].ID != 2 {
		t.Fatalf("unknown-model routing = %#v, want both accounts", unknownModel)
	}
}

func TestExcludeAccountsDoesNotMutateCandidates(t *testing.T) {
	t.Parallel()

	candidates := []*ent.Account{{ID: 1}, {ID: 2}, {ID: 3}}
	got := excludeAccounts(candidates, []int{2})

	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
		t.Fatalf("excludeAccounts result = %+v, want IDs [1 3]", got)
	}
	if len(candidates) != 3 || candidates[0].ID != 1 || candidates[1].ID != 2 || candidates[2].ID != 3 {
		t.Fatalf("candidates mutated to %+v, want original IDs [1 2 3]", candidates)
	}
}

func TestNormalizeGroupLookupErrorPreservesCancellation(t *testing.T) {
	t.Parallel()

	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		got := normalizeGroupLookupError(err)
		if !errors.Is(got, err) {
			t.Fatalf("normalizeGroupLookupError(%v) = %v, want original error", err, got)
		}
	}
}

func TestNormalizeGroupLookupErrorWrapsGenericError(t *testing.T) {
	t.Parallel()

	orig := errors.New("db offline")
	got := normalizeGroupLookupError(orig)
	if errors.Is(got, ErrGroupNotFound) {
		t.Fatalf("normalizeGroupLookupError(%v) = %v, want generic query error", orig, got)
	}
	if got.Error() != "查询分组失败: db offline" {
		t.Fatalf("normalizeGroupLookupError(%v) = %q, want %q", orig, got.Error(), "查询分组失败: db offline")
	}
}

func TestNormalizeGroupAccountsLookupErrorPreservesCancellation(t *testing.T) {
	t.Parallel()

	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		got := normalizeGroupAccountsLookupError(err)
		if !errors.Is(got, err) {
			t.Fatalf("normalizeGroupAccountsLookupError(%v) = %v, want original error", err, got)
		}
	}
}

func TestNormalizeGroupAccountsLookupErrorWrapsGenericError(t *testing.T) {
	t.Parallel()

	orig := errors.New("db offline")
	got := normalizeGroupAccountsLookupError(orig)
	if got.Error() != "查询分组账户失败: db offline" {
		t.Fatalf("normalizeGroupAccountsLookupError(%v) = %q, want %q", orig, got.Error(), "查询分组账户失败: db offline")
	}
}
