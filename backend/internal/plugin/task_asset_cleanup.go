package plugin

import (
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/DouDOU-start/airgate-core/ent"
)

const (
	taskInputAssetObjectKeysField  = "_input_asset_object_keys"
	taskOutputAssetObjectKeysField = "asset_object_keys"
	runtimeAssetURLPrefix          = "/assets-runtime/"
)

var runtimeAssetURLRegexp = regexp.MustCompile(`(?i)/assets-runtime/[^\s"'<>)]+`)

func collectTaskAssetObjectKeys(t *ent.Task) []string {
	if t == nil {
		return nil
	}
	seen := make(map[string]struct{})
	addStringValues(seen, taskInputAssetObjectKeysField, t.Attributes)
	addStringValues(seen, taskOutputAssetObjectKeysField, t.Output)
	collectRuntimeAssetObjectKeys(seen, t.Output)

	if len(seen) == 0 {
		return nil
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func addStringValues(seen map[string]struct{}, field string, values map[string]interface{}) {
	if len(values) == 0 {
		return
	}
	addStringValue(seen, values[field])
}

func addStringValue(seen map[string]struct{}, value any) {
	switch v := value.(type) {
	case string:
		if v != "" {
			seen[v] = struct{}{}
		}
	case []string:
		for _, item := range v {
			addStringValue(seen, item)
		}
	case []any:
		for _, item := range v {
			addStringValue(seen, item)
		}
	case map[string]any:
		for _, item := range v {
			addStringValue(seen, item)
		}
	}
}

func collectRuntimeAssetObjectKeys(seen map[string]struct{}, value any) {
	switch v := value.(type) {
	case string:
		for _, ref := range runtimeAssetURLRegexp.FindAllString(v, -1) {
			if objectKey, err := runtimeAssetURLToObjectKey(ref); err == nil && objectKey != "" {
				seen[objectKey] = struct{}{}
			}
		}
	case []any:
		for _, item := range v {
			collectRuntimeAssetObjectKeys(seen, item)
		}
	case []string:
		for _, item := range v {
			collectRuntimeAssetObjectKeys(seen, item)
		}
	case map[string]any:
		for _, item := range v {
			collectRuntimeAssetObjectKeys(seen, item)
		}
	}
}

func runtimeAssetURLToObjectKey(ref string) (string, error) {
	rest := strings.TrimPrefix(ref, runtimeAssetURLPrefix)
	if rest == "" {
		return "", nil
	}
	if q := strings.IndexByte(rest, '?'); q >= 0 {
		rest = rest[:q]
	}
	parts := strings.Split(rest, "/")
	for i, part := range parts {
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return "", err
		}
		parts[i] = decoded
	}
	return strings.Join(parts, "/"), nil
}
