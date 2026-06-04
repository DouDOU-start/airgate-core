package scheduler

import (
	"strings"

	"github.com/DouDOU-start/airgate-core/ent"
)

type Workload string

const (
	WorkloadChat  Workload = "chat"
	WorkloadImage Workload = "image"
)

type ImageProtocol string

const (
	ImageProtocolImagesAPI     ImageProtocol = "images_api"
	ImageProtocolResponsesTool ImageProtocol = "responses_tool"
)

type AccountRequirements struct {
	Workload       Workload
	ImageProtocols []ImageProtocol
}

func filterAccountsByRequirements(candidates []*ent.Account, req AccountRequirements) []*ent.Account {
	if req.Workload == "" && len(req.ImageProtocols) == 0 {
		return candidates
	}
	filtered := make([]*ent.Account, 0, len(candidates))
	for _, acc := range candidates {
		if accountMatchesRequirements(acc, req) {
			filtered = append(filtered, acc)
		}
	}
	return filtered
}

func accountMatchesRequirements(acc *ent.Account, req AccountRequirements) bool {
	if acc == nil {
		return false
	}
	if req.Workload != "" && !accountAllowsWorkload(acc, req.Workload) {
		return false
	}
	if len(req.ImageProtocols) > 0 && !accountAllowsAnyImageProtocol(acc, req.ImageProtocols) {
		return false
	}
	return true
}

func accountAllowsWorkload(acc *ent.Account, workload Workload) bool {
	allowed := extraStringSet(acc.Extra, "allowed_workloads")
	if len(allowed) == 0 {
		return true
	}
	_, ok := allowed[string(workload)]
	return ok
}

func accountAllowsAnyImageProtocol(acc *ent.Account, required []ImageProtocol) bool {
	allowed := accountImageProtocolSet(acc)
	for _, protocol := range required {
		if _, ok := allowed[string(protocol)]; ok {
			return true
		}
	}
	return false
}

func accountImageProtocolSet(acc *ent.Account) map[string]struct{} {
	allowed := extraStringSet(acc.Extra, "image_protocols")
	if len(allowed) > 0 {
		return allowed
	}
	if acc != nil {
		if strings.TrimSpace(acc.Credentials["access_token"]) != "" {
			return map[string]struct{}{string(ImageProtocolResponsesTool): {}}
		}
		if strings.TrimSpace(acc.Credentials["api_key"]) != "" {
			return map[string]struct{}{string(ImageProtocolImagesAPI): {}}
		}
	}
	return map[string]struct{}{
		string(ImageProtocolImagesAPI):     {},
		string(ImageProtocolResponsesTool): {},
	}
}

func AccountImageProtocols(acc *ent.Account) []string {
	set := accountImageProtocolSet(acc)
	if len(set) == 0 {
		return nil
	}
	protocols := make([]string, 0, len(set))
	for _, protocol := range []string{string(ImageProtocolImagesAPI), string(ImageProtocolResponsesTool)} {
		if _, ok := set[protocol]; ok {
			protocols = append(protocols, protocol)
			delete(set, protocol)
		}
	}
	for protocol := range set {
		protocols = append(protocols, protocol)
	}
	return protocols
}

func extraStringSet(extra map[string]interface{}, key string) map[string]struct{} {
	if len(extra) == 0 {
		return nil
	}
	raw, ok := extra[key]
	if !ok || raw == nil {
		return nil
	}
	values := make(map[string]struct{})
	add := func(value string) {
		for _, part := range strings.Split(value, ",") {
			normalized := strings.ToLower(strings.TrimSpace(part))
			if normalized != "" {
				values[normalized] = struct{}{}
			}
		}
	}
	switch v := raw.(type) {
	case string:
		add(v)
	case []string:
		for _, item := range v {
			add(item)
		}
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				add(s)
			}
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}
