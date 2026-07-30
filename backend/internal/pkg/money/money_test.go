package money

import "testing"

func TestParseAndFormat(t *testing.T) {
	for raw, want := range map[string]int64{
		"0": 0, "1": Scale, "1.5": 150_000_000, "0.00000001": 1,
	} {
		got, err := Parse(raw)
		if err != nil || got != want {
			t.Fatalf("Parse(%q) = %d, %v; want %d", raw, got, err, want)
		}
		if round, err := Parse(FormatUnits(got)); err != nil || round != want {
			t.Fatalf("round trip %q = %d, %v", raw, round, err)
		}
	}
}
