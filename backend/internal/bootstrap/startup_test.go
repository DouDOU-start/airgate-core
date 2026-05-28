package bootstrap

import "testing"

func TestSplitSQLStatements(t *testing.T) {
	sql := `
-- comment ; stays with next statement
SELECT 'a;b';
DO $$
BEGIN
	RAISE NOTICE 'x;y';
END $$;
/* block ; comment */
CREATE INDEX CONCURRENTLY idx_example ON public.example (created_at);
`

	got := splitSQLStatements(sql)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3: %#v", len(got), got)
	}
	if got[0] != "-- comment ; stays with next statement\nSELECT 'a;b'" {
		t.Fatalf("stmt[0] = %q", got[0])
	}
	if got[1] != "DO $$\nBEGIN\n\tRAISE NOTICE 'x;y';\nEND $$" {
		t.Fatalf("stmt[1] = %q", got[1])
	}
	if got[2] != "/* block ; comment */\nCREATE INDEX CONCURRENTLY idx_example ON public.example (created_at)" {
		t.Fatalf("stmt[2] = %q", got[2])
	}
}

func TestValidateSystemUpgradeFilename(t *testing.T) {
	valid := "20260528143015_usage_logs_upgrade.sql"
	if err := validateSystemUpgradeFilename(valid); err != nil {
		t.Fatalf("valid filename rejected: %v", err)
	}

	invalid := []string{
		"20260528_usage_logs_upgrade.sql",
		"202605281430_usage_logs_upgrade.sql",
		"20260528143015.sql",
		"20261328143015_usage_logs_upgrade.sql",
	}
	for _, name := range invalid {
		if err := validateSystemUpgradeFilename(name); err == nil {
			t.Fatalf("invalid filename accepted: %s", name)
		}
	}
}
