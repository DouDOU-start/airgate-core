package plugin

import (
	"reflect"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/plugin/hookv2"
)

func TestRelayHookV2RichConfigSchemaPreservesFields(t *testing.T) {
	input := &hookv2.ConfigSchema{
		Version: "4",
		Fields: []hookv2.ConfigField{
			{
				Key:         "group_ids",
				FallbackKey: "legacy_group_ids",
				Label:       "生效分组",
				Description: "仅应用到选中的分组",
				Widget:      "multi_select",
				DataSource:  "groups",
				Required:    true,
				Default:     []any{float64(1), float64(2)},
				Filter:      map[string]string{"platform": "example"},
			},
		},
	}

	got := relayHookV2RichConfigSchema(input)
	if got == nil || got.Version != "4" || len(got.Fields) != 1 {
		t.Fatalf("rich config schema = %#v", got)
	}
	field := got.Fields[0]
	if field.Key != "group_ids" || field.FallbackKey != "legacy_group_ids" || field.Type != "string" ||
		field.Widget != "multi_select" || field.DataSource != "groups" || !field.Required ||
		field.Filter["platform"] != "example" || !reflect.DeepEqual(field.Default, []any{float64(1), float64(2)}) {
		t.Fatalf("rich config field = %#v", field)
	}

	input.Fields[0].Filter["platform"] = "changed"
	input.Fields[0].Default.([]any)[0] = float64(99)
	if field.Filter["platform"] != "example" || !reflect.DeepEqual(field.Default, []any{float64(1), float64(2)}) {
		t.Fatalf("rich config schema was not cloned: %#v", field)
	}
}
