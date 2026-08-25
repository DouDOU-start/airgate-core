package pluginadmin

import (
	"context"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/plugin"
)

func TestReloadRejectsNonDevPlugin(t *testing.T) {
	service := NewService(pluginAdminManagerStub{}, pluginMarketplaceStub{})
	if err := service.Reload(t.Context(), "demo"); err != ErrPluginNotDev {
		t.Fatalf("Reload() error = %v, want %v", err, ErrPluginNotDev)
	}
}

func TestListPreservesRichConfigSchema(t *testing.T) {
	schema := &plugin.PluginConfigSchema{
		Version: "4",
		Fields: []plugin.PluginConfigField{{
			Key:        "group_ids",
			Widget:     "multi_select",
			DataSource: "groups",
			Default:    []int{1, 2},
			Filter:     map[string]string{"platform": "example"},
		}},
	}
	service := NewService(pluginAdminManagerStub{
		allMeta: []plugin.PluginMeta{{Name: "relay-hook-v2", RichConfigSchema: schema}},
	}, pluginMarketplaceStub{})

	items := service.List()
	if len(items) != 1 || items[0].RichConfigSchema == nil || len(items[0].RichConfigSchema.Fields) != 1 {
		t.Fatalf("List() rich config schema = %#v", items)
	}
	schema.Fields[0].Filter["platform"] = "changed"
	schema.Fields[0].Default.([]int)[0] = 99
	field := items[0].RichConfigSchema.Fields[0]
	defaults, ok := field.Default.([]int)
	if field.Filter["platform"] != "example" || !ok || len(defaults) != 2 || defaults[0] != 1 {
		t.Fatalf("List() rich config schema was not cloned: %#v", field)
	}
}

func TestListMarketplaceMarksInstalled(t *testing.T) {
	service := NewService(pluginAdminManagerStub{
		allMeta: []plugin.PluginMeta{{Name: "gateway-openai"}},
	}, pluginMarketplaceStub{
		listAvailable: func(context.Context) ([]plugin.MarketplacePlugin, error) {
			return []plugin.MarketplacePlugin{{Name: "gateway-openai"}, {Name: "gateway-gemini"}}, nil
		},
	})

	items, err := service.ListMarketplace(t.Context())
	if err != nil {
		t.Fatalf("ListMarketplace() error = %v", err)
	}
	if len(items) != 2 || !items[0].Installed || items[1].Installed {
		t.Fatalf("unexpected marketplace items: %+v", items)
	}
}

func TestListMarketplaceDoesNotOfferUpdatesForDevPlugin(t *testing.T) {
	service := NewService(pluginAdminManagerStub{
		allMeta: []plugin.PluginMeta{{Name: "airgate-playground", Version: "0.1.0", IsDev: true}},
	}, pluginMarketplaceStub{
		listAvailable: func(context.Context) ([]plugin.MarketplacePlugin, error) {
			return []plugin.MarketplacePlugin{{Name: "airgate-playground", Version: "0.1.10"}}, nil
		},
	})

	items, err := service.ListMarketplace(t.Context())
	if err != nil {
		t.Fatalf("ListMarketplace() error = %v", err)
	}
	if len(items) != 1 || !items[0].Installed || items[0].HasUpdate {
		t.Fatalf("unexpected marketplace items: %+v", items)
	}
}

type pluginAdminManagerStub struct {
	allMeta []plugin.PluginMeta
}

func (s pluginAdminManagerStub) GetAllPluginMeta() []plugin.PluginMeta {
	return append([]plugin.PluginMeta(nil), s.allMeta...)
}
func (s pluginAdminManagerStub) InstallFromBinary(context.Context, string, []byte) error { return nil }
func (s pluginAdminManagerStub) InstallFromGithub(context.Context, string) error         { return nil }
func (s pluginAdminManagerStub) Uninstall(context.Context, string) error                 { return nil }
func (s pluginAdminManagerStub) ReloadDev(context.Context, string) error                 { return nil }
func (s pluginAdminManagerStub) ReloadInstance(context.Context, string) error            { return nil }
func (s pluginAdminManagerStub) IsDev(string) bool                                       { return false }
func (s pluginAdminManagerStub) GetInstance(string) *plugin.PluginInstance               { return nil }
func (s pluginAdminManagerStub) GetPluginConfig(context.Context, string) (map[string]string, error) {
	return nil, nil
}
func (s pluginAdminManagerStub) UpdatePluginConfig(context.Context, string, map[string]string) error {
	return nil
}

type pluginMarketplaceStub struct {
	listAvailable func(context.Context) ([]plugin.MarketplacePlugin, error)
}

func (s pluginMarketplaceStub) ListAvailable(ctx context.Context) ([]plugin.MarketplacePlugin, error) {
	if s.listAvailable == nil {
		return nil, nil
	}
	return s.listAvailable(ctx)
}

func (s pluginMarketplaceStub) SyncFromGithub(context.Context) error {
	return nil
}
