package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

// maxCodexClientVersionLength bounds the cache-key input supplied by the
// client while accepting the version strings used by released Codex CLIs.
const maxCodexClientVersionLength = 128

// codexModelsResponse is deliberately separate from modelList. Codex's
// models-manager expects the top-level `models` member and decodes each entry
// as the openai_models::ModelInfo structure, whereas the legacy OpenAI route
// must continue returning {object:"list",data:[...]}.
type codexModelsResponse struct {
	Models []codexModelInfo `json:"models"`
}

type codexReasoningLevel struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

type codexTruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int64  `json:"limit"`
}

// codexModelMessages mirrors the nullable sections in the current Codex
// ModelMessages wire contract.  AirGate does not own OpenAI's prompt,
// Guardian, or confirmation-policy catalog, so it publishes an explicit
// empty instruction template and null optional sections instead of inventing
// model-owned policy text.  Keeping every non-default Option field present is
// important: once model_messages itself is present, serde expects these keys
// even though their values may be null.
type codexModelMessages struct {
	InstructionsTemplate  *string `json:"instructions_template"`
	InstructionsVariables any     `json:"instructions_variables"`
	Approvals             any     `json:"approvals"`
	CollaborationModes    any     `json:"collaboration_modes"`
	AutoReview            any     `json:"auto_review"`
	Permissions           any     `json:"permissions"`
	MultiAgent            any     `json:"multi_agent"`
	GuardianV2            any     `json:"guardian_v2"`
	ConfirmationPolicies  any     `json:"confirmation_policies"`
}

// codexModelInfo mirrors the wire fields consumed by the current Codex CLI.
// Nullable fields without serde defaults are emitted explicitly (as null) so
// newer clients can deserialize the response without relying on version-
// specific defaults. Extra fields with defaults are included as stable,
// conservative values to make the endpoint useful for custom model slugs too.
type codexModelInfo struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	// The current Codex models decoder requires either this legacy field or a
	// model_messages.instructions_template.  AirGate does not own the official
	// model prompt catalog, so emit an explicit empty legacy value instead of
	// inventing prompt metadata.  An empty string is still a valid template and
	// keeps the response deserializable by current and older CLIs.
	BaseInstructions               string                `json:"base_instructions"`
	DefaultReasoningLevel          string                `json:"default_reasoning_level"`
	SupportedReasoningLevels       []codexReasoningLevel `json:"supported_reasoning_levels"`
	ShellType                      string                `json:"shell_type"`
	Visibility                     string                `json:"visibility"`
	SupportedInAPI                 bool                  `json:"supported_in_api"`
	Priority                       int                   `json:"priority"`
	AdditionalSpeedTiers           []string              `json:"additional_speed_tiers"`
	ServiceTiers                   []codexServiceTier    `json:"service_tiers"`
	DefaultServiceTier             *string               `json:"default_service_tier"`
	AvailabilityNUX                any                   `json:"availability_nux"`
	Upgrade                        any                   `json:"upgrade"`
	ModelMessages                  codexModelMessages    `json:"model_messages"`
	IncludeSkillsUsageInstructions bool                  `json:"include_skills_usage_instructions"`
	IncludePluginUsageInstructions bool                  `json:"include_plugin_usage_instructions"`
	IncludeAppsUsageInstructions   bool                  `json:"include_apps_usage_instructions"`
	SupportsReasoningSummary       bool                  `json:"supports_reasoning_summary_parameter"`
	DefaultReasoningSummary        string                `json:"default_reasoning_summary"`
	SupportVerbosity               bool                  `json:"support_verbosity"`
	DefaultVerbosity               any                   `json:"default_verbosity"`
	ApplyPatchToolType             any                   `json:"apply_patch_tool_type"`
	WebSearchToolType              string                `json:"web_search_tool_type"`
	TruncationPolicy               codexTruncationPolicy `json:"truncation_policy"`
	SupportsImageDetailOriginal    bool                  `json:"supports_image_detail_original"`
	ContextWindow                  *int64                `json:"context_window"`
	MaxContextWindow               *int64                `json:"max_context_window,omitempty"`
	AutoCompactTokenLimit          *int64                `json:"auto_compact_token_limit,omitempty"`
	CompHash                       *string               `json:"comp_hash,omitempty"`
	EffectiveContextWindowPercent  int                   `json:"effective_context_window_percent"`
	ExperimentalSupportedTools     []string              `json:"experimental_supported_tools"`
	InputModalities                []string              `json:"input_modalities"`
	SupportsSearchTool             bool                  `json:"supports_search_tool"`
	UseResponsesLite               bool                  `json:"use_responses_lite"`
	NodeReplAutoReviewRequired     bool                  `json:"node_repl_auto_review_required"`
	NodeReplDisabled               bool                  `json:"node_repl_disabled"`
	AutoReviewModelOverride        *string               `json:"auto_review_model_override,omitempty"`
	ModelSpecialty                 *string               `json:"model_specialty,omitempty"`
	ToolMode                       *string               `json:"tool_mode,omitempty"`
	MultiAgentVersion              *string               `json:"multi_agent_version,omitempty"`
	MultiAgentReasoningEffort      *string               `json:"multi_agent_reasoning_effort"`
}

type codexServiceTier struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// HandleCodexModels serves the versioned Codex model catalog at
// /codex/v1/models. The model set is scoped to the authenticated API key's
// group and uses the same priced/available aggregation as /v1/models, while
// projecting only entries usable through the OpenAI protocol.
func (p *Pipeline) HandleCodexModels(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolOpenAI)

	clientVersion := strings.TrimSpace(c.Query("client_version"))
	if len(clientVersion) > maxCodexClientVersionLength {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_client_version", "client_version is too long")
		return
	}

	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}

	entries := p.modelEntriesForGroup(keyInfo.GroupID)
	models := codexModelInfos(entries)
	payload := codexModelsResponse{Models: models}
	body, err := json.Marshal(payload)
	if err != nil {
		// The response is composed entirely of primitive values, so this should
		// be unreachable. Keep the failure in the protocol's normal error shape.
		writeError(c, http.StatusInternalServerError, "server_error", "models_encode_failed", "failed to encode Codex model catalog")
		return
	}

	etag := codexModelsETag(clientVersion, body)
	c.Header("ETag", etag)
	// Codex carries the catalog validator on Responses responses as
	// `X-Models-Etag`.  Keep the two validators identical so a client can
	// compare a response's catalog revision with a subsequent /models fetch.
	c.Header("X-Models-Etag", etag)
	c.Header("Cache-Control", "private, max-age=0, must-revalidate")
	if codexIfNoneMatch(c.GetHeader("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, "application/json", body)
}

// codexModelInfos projects the existing group model directory into the
// official Codex ModelInfo wire shape. Entries are sorted defensively because
// callers outside modelEntriesForGroup may provide an arbitrary order.
func codexModelInfos(entries []registry.ModelEntry) []codexModelInfo {
	metadata := codexCatalogMetadata()
	filtered := make([]registry.ModelEntry, 0, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		if name == "" || !slices.Contains(entry.Protocols, registry.ProtocolOpenAI) {
			continue
		}
		entry.Name = name
		filtered = append(filtered, entry)
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Name < filtered[j].Name })

	models := make([]codexModelInfo, 0, len(filtered))
	for index, entry := range filtered {
		info, found := metadata[entry.Name]
		models = append(models, newCodexModelInfo(entry.Name, info, found, index+1))
	}
	return models
}

// codexCatalogMetadata combines all plan buckets. The runtime directory is
// already filtered by the authenticated group, so metadata lookup is only a
// descriptive projection and must not hide custom or plan-specific entries.
func codexCatalogMetadata() map[string]cpa.ModelInfo {
	result := make(map[string]cpa.ModelInfo)
	for _, plan := range []string{"free", "plus", "team", "pro"} {
		for _, info := range cpa.DefaultModelInfos("codex", plan) {
			id := strings.TrimSpace(info.ID)
			if id == "" {
				continue
			}
			info.ID = id
			if existing, exists := result[id]; exists {
				// A model can occur in more than one plan bucket.  Do not let a
				// sparse entry from a later bucket erase fields from the bundled
				// baseline; merge only meaningful values instead.
				result[id] = mergeCodexModelMetadata(existing, info)
			} else {
				result[id] = cloneCodexModelMetadata(info)
			}
		}
	}
	return result
}

// mergeCodexModelMetadata overlays a possibly incomplete catalog entry on a
// bundled entry.  Empty strings, zero numeric values, and empty slices mean
// "not supplied" for this projection and therefore never erase a richer
// bundled value.  Repeated model entries are common across plan buckets, so
// this is deliberately field-wise rather than whole-struct replacement.
func mergeCodexModelMetadata(base, overlay cpa.ModelInfo) cpa.ModelInfo {
	merged := cloneCodexModelMetadata(base)
	if strings.TrimSpace(merged.ID) == "" {
		merged.ID = strings.TrimSpace(overlay.ID)
	}

	if strings.TrimSpace(merged.DisplayName) == "" {
		merged.DisplayName = strings.TrimSpace(overlay.DisplayName)
	}
	if strings.TrimSpace(merged.OwnedBy) == "" {
		merged.OwnedBy = strings.TrimSpace(overlay.OwnedBy)
	}
	if strings.TrimSpace(merged.Type) == "" {
		merged.Type = strings.TrimSpace(overlay.Type)
	}
	if strings.TrimSpace(merged.Description) == "" {
		merged.Description = strings.TrimSpace(overlay.Description)
	}

	// A larger positive limit is the only safe way to combine plan entries:
	// choosing the maximum avoids under-reporting a capability while still
	// ignoring sparse zero values.
	if overlay.ContextLength > merged.ContextLength {
		merged.ContextLength = overlay.ContextLength
	}
	if overlay.MaxCompletionTokens > merged.MaxCompletionTokens {
		merged.MaxCompletionTokens = overlay.MaxCompletionTokens
	}
	merged.Thinking.Levels = mergeCodexStringSet(merged.Thinking.Levels, overlay.Thinking.Levels)
	merged.SupportedInputModalities = mergeCodexStringSet(
		merged.SupportedInputModalities,
		overlay.SupportedInputModalities,
	)
	return merged
}

func cloneCodexModelMetadata(info cpa.ModelInfo) cpa.ModelInfo {
	clone := info
	clone.ID = strings.TrimSpace(clone.ID)
	clone.DisplayName = strings.TrimSpace(clone.DisplayName)
	clone.OwnedBy = strings.TrimSpace(clone.OwnedBy)
	clone.Type = strings.TrimSpace(clone.Type)
	clone.Description = strings.TrimSpace(clone.Description)
	clone.Thinking.Levels = append([]string(nil), info.Thinking.Levels...)
	clone.SupportedInputModalities = append([]string(nil), info.SupportedInputModalities...)
	return clone
}

func mergeCodexStringSet(base, overlay []string) []string {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	result := make([]string, 0, len(base)+len(overlay))
	seen := make(map[string]struct{}, len(base)+len(overlay))
	for _, values := range [][]string{base, overlay} {
		for _, raw := range values {
			value := strings.ToLower(strings.TrimSpace(raw))
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}

func newCodexModelInfo(slug string, metadata cpa.ModelInfo, known bool, priority int) codexModelInfo {
	if !known {
		metadata = cpa.ModelInfo{ID: slug, DisplayName: slug}
	}

	displayName := strings.TrimSpace(metadata.DisplayName)
	if displayName == "" {
		displayName = slug
	}
	description := strings.TrimSpace(metadata.Description)
	if description == "" {
		description = "Available through AirGate."
	}

	levels := codexReasoningLevels(metadata.Thinking.Levels)
	defaultLevel := "medium"
	if strings.EqualFold(slug, "gpt-5.6-sol") || strings.Contains(strings.ToLower(slug), "daybreak-blue") {
		defaultLevel = "low"
	}
	if !containsReasoningLevel(levels, defaultLevel) {
		defaultLevel = levels[0].Effort
	}

	inputModalities := normalizeInputModalities(metadata.SupportedInputModalities)
	contextWindow := metadata.ContextLength
	if contextWindow <= 0 {
		contextWindow = 272000
	}
	context := contextWindow

	visibility := "list"
	if slug == "codex-auto-review" {
		visibility = "hide"
	}
	emptyInstructions := ""

	return codexModelInfo{
		Slug:                     slug,
		DisplayName:              displayName,
		Description:              description,
		BaseInstructions:         "",
		DefaultReasoningLevel:    defaultLevel,
		SupportedReasoningLevels: levels,
		ShellType:                "unified_exec",
		Visibility:               visibility,
		SupportedInAPI:           true,
		Priority:                 priority,
		AdditionalSpeedTiers:     []string{},
		ServiceTiers:             []codexServiceTier{},
		DefaultServiceTier:       nil,
		AvailabilityNUX:          nil,
		Upgrade:                  nil,
		ModelMessages: codexModelMessages{
			InstructionsTemplate: &emptyInstructions,
		},
		IncludeSkillsUsageInstructions: false,
		IncludePluginUsageInstructions: false,
		IncludeAppsUsageInstructions:   true,
		SupportsReasoningSummary:       true,
		DefaultReasoningSummary:        "auto",
		SupportVerbosity:               false,
		DefaultVerbosity:               nil,
		ApplyPatchToolType:             nil,
		WebSearchToolType:              "text",
		TruncationPolicy:               codexTruncationPolicy{Mode: "tokens", Limit: 10000},
		SupportsImageDetailOriginal:    false,
		ContextWindow:                  &context,
		MaxContextWindow:               &context,
		EffectiveContextWindowPercent:  95,
		ExperimentalSupportedTools:     []string{},
		InputModalities:                inputModalities,
		SupportsSearchTool:             false,
		UseResponsesLite:               false,
		NodeReplAutoReviewRequired:     false,
		NodeReplDisabled:               false,
		MultiAgentReasoningEffort:      nil,
	}
}

func codexReasoningLevels(raw []string) []codexReasoningLevel {
	levels := make([]codexReasoningLevel, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		effort := strings.ToLower(strings.TrimSpace(value))
		if effort == "" {
			continue
		}
		if _, exists := seen[effort]; exists {
			continue
		}
		seen[effort] = struct{}{}
		levels = append(levels, codexReasoningLevel{Effort: effort, Description: effort})
	}
	if len(levels) == 0 {
		for _, effort := range []string{"low", "medium", "high", "xhigh"} {
			levels = append(levels, codexReasoningLevel{Effort: effort, Description: effort})
		}
	}
	return levels
}

func containsReasoningLevel(levels []codexReasoningLevel, effort string) bool {
	for _, level := range levels {
		if level.Effort == effort {
			return true
		}
	}
	return false
}

func normalizeInputModalities(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	result := make([]string, 0, len(raw))
	for _, value := range raw {
		modality := strings.ToLower(strings.TrimSpace(value))
		switch modality {
		case "text", "image", "audio":
			if _, exists := seen[modality]; !exists {
				seen[modality] = struct{}{}
				result = append(result, modality)
			}
		}
	}
	if len(result) == 0 {
		return []string{"text", "image"}
	}
	return result
}

func codexModelsETag(_ string, body []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("airgate-codex-models-v1\x00"))
	_, _ = hash.Write(body)
	return `"` + hex.EncodeToString(hash.Sum(nil)) + `"`
}

// codexIfNoneMatch implements the GET/HEAD weak comparison defined for
// If-None-Match: exact tags, comma-separated lists, weak tags, and '*'.
func codexIfNoneMatch(header, current string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if candidate == "*" {
			return true
		}
		if len(candidate) >= 2 && strings.EqualFold(candidate[:2], "w/") {
			candidate = strings.TrimSpace(candidate[2:])
		}
		if candidate == current {
			return true
		}
	}
	return false
}
