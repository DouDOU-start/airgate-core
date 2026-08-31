package transport

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
)

// maxCodexImageSSEEventBytes keeps usage observation bounded while image data
// frames themselves continue to pass through unchanged. A malformed provider
// event must never turn a streaming relay into an unbounded accumulator.
const maxCodexImageSSEEventBytes = 1 << 20

// codexImageUsageObserver extracts the optional accounting metadata from the
// OpenAI Images streaming contract. It is deliberately side-band: Feed never
// changes the bytes that the caller receives.
type codexImageUsageObserver struct {
	pending      []byte
	event        bytes.Buffer
	observing    bool
	usage        *dto.Usage
	calls        int
	imageSize    string
	imageQuality string
}

func newCodexImageUsageObserver() *codexImageUsageObserver {
	return &codexImageUsageObserver{observing: true}
}

func (o *codexImageUsageObserver) Feed(data []byte) {
	if o == nil || len(data) == 0 {
		return
	}
	o.pending = append(o.pending, data...)
	for {
		index := bytes.IndexByte(o.pending, '\n')
		if index < 0 {
			// Keep a small incomplete line for the next frame. A provider that
			// sends a giant unterminated line is not allowed to consume memory.
			if len(o.pending) > maxCodexImageSSEEventBytes {
				o.pending = o.pending[len(o.pending)-maxCodexImageSSEEventBytes:]
				o.observing = false
			}
			return
		}
		line := append([]byte(nil), o.pending[:index]...)
		o.pending = o.pending[index+1:]
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if o.observing {
			if o.event.Len()+len(line)+1 <= maxCodexImageSSEEventBytes {
				_, _ = o.event.Write(line)
				_ = o.event.WriteByte('\n')
			} else {
				o.observing = false
				o.event.Reset()
			}
		}
		if len(bytes.TrimSpace(line)) == 0 {
			if o.observing {
				o.observeEvent(o.event.Bytes())
			}
			o.event.Reset()
			o.observing = true
		}
	}
}

func (o *codexImageUsageObserver) Finish() {
	if o == nil {
		return
	}
	if len(o.pending) > 0 {
		line := append([]byte(nil), o.pending...)
		o.pending = nil
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if o.observing && o.event.Len()+len(line)+1 <= maxCodexImageSSEEventBytes {
			_, _ = o.event.Write(line)
			_ = o.event.WriteByte('\n')
		}
	}
	if o.observing && o.event.Len() > 0 {
		o.observeEvent(o.event.Bytes())
	}
	o.event.Reset()
}

func (o *codexImageUsageObserver) observeEvent(event []byte) {
	if o == nil {
		return
	}
	var payload bytes.Buffer
	for _, line := range strings.Split(strings.ReplaceAll(string(event), "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		part := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if part == "" || part == "[DONE]" {
			continue
		}
		if payload.Len() > 0 {
			_ = payload.WriteByte('\n')
		}
		_, _ = payload.WriteString(part)
	}
	if payload.Len() == 0 {
		return
	}
	o.observeJSON(payload.Bytes())
}

func (o *codexImageUsageObserver) observeJSON(raw []byte) {
	if o == nil {
		return
	}
	var value struct {
		Type       string            `json:"type"`
		Usage      json.RawMessage   `json:"usage"`
		Size       string            `json:"size"`
		Resolution string            `json:"resolution"`
		Quality    string            `json:"quality"`
		Data       []json.RawMessage `json:"data"`
		Response   struct {
			Usage json.RawMessage `json:"usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return
	}
	if strings.HasSuffix(strings.ToLower(strings.TrimSpace(value.Type)), ".completed") {
		o.calls++
	}
	if len(value.Data) > 0 && o.calls == 0 {
		// A few image gateways return a complete Images response as one SSE
		// event without the official type marker.
		o.calls = len(value.Data)
	}
	if value.Size != "" {
		o.imageSize = value.Size
	} else if value.Resolution != "" {
		o.imageSize = value.Resolution
	}
	if value.Quality != "" {
		o.imageQuality = value.Quality
	}
	usage := value.Usage
	if len(usage) == 0 || string(usage) == "null" {
		usage = value.Response.Usage
	}
	if parsed, ok := parseCodexImageUsage(usage); ok {
		o.mergeUsage(parsed)
	}
}

func (o *codexImageUsageObserver) mergeUsage(in dto.Usage) {
	if o == nil {
		return
	}
	if o.usage == nil {
		o.usage = &dto.Usage{}
	}
	if in.PromptTokens != 0 {
		o.usage.PromptTokens = in.PromptTokens
	}
	if in.CompletionTokens != 0 {
		o.usage.CompletionTokens = in.CompletionTokens
	}
	if in.CachedTokens != 0 {
		o.usage.CachedTokens = in.CachedTokens
	}
	if in.CacheCreationTokens != 0 {
		o.usage.CacheCreationTokens = in.CacheCreationTokens
	}
	if in.CacheCreation5mTokens != 0 {
		o.usage.CacheCreation5mTokens = in.CacheCreation5mTokens
	}
	if in.CacheCreation1hTokens != 0 {
		o.usage.CacheCreation1hTokens = in.CacheCreation1hTokens
	}
	if in.ImageSize != "" {
		o.usage.ImageSize = in.ImageSize
	}
	if in.ImageQuality != "" {
		o.usage.ImageQuality = in.ImageQuality
	}
}

func (o *codexImageUsageObserver) Usage() *dto.Usage {
	if o == nil {
		return nil
	}
	if o.usage == nil && o.calls == 0 && o.imageSize == "" && o.imageQuality == "" {
		return nil
	}
	result := dto.Usage{}
	if o.usage != nil {
		result = *o.usage
	}
	result.Calls = o.calls
	result.ImageSize = o.imageSize
	result.ImageQuality = o.imageQuality
	return &result
}

// parseCodexImageUsage parses a JSON Images response while preserving all
// response bytes. It is used for non-streaming native calls, where the Core
// transport has the complete body available before returning to the pipeline.
func parseCodexImageResponseUsage(body []byte) *dto.Usage {
	if len(body) == 0 {
		return nil
	}
	var value struct {
		Usage      json.RawMessage   `json:"usage"`
		Data       []json.RawMessage `json:"data"`
		Size       string            `json:"size"`
		Resolution string            `json:"resolution"`
		Quality    string            `json:"quality"`
	}
	if json.Unmarshal(body, &value) != nil {
		return nil
	}
	var result dto.Usage
	if parsed, ok := parseCodexImageUsage(value.Usage); ok {
		result = parsed
	}
	if len(value.Data) > 0 {
		result.Calls = len(value.Data)
	}
	if value.Size != "" {
		result.ImageSize = value.Size
	} else {
		result.ImageSize = value.Resolution
	}
	result.ImageQuality = value.Quality
	if result.Calls == 0 && result.PromptTokens == 0 && result.CompletionTokens == 0 && result.ImageSize == "" && result.ImageQuality == "" {
		return nil
	}
	return &result
}

// mergeCodexImageUsage keeps token fields emitted by the plugin and fills in
// image-specific accounting fields observed by Core. Non-zero values from the
// latter take precedence because they describe the concrete image response
// (for example, the output size/quality rather than a request default).
func mergeCodexImageUsage(base, overlay *dto.Usage) *dto.Usage {
	if base == nil && overlay == nil {
		return nil
	}
	merged := dto.Usage{}
	if base != nil {
		merged = *base
	}
	if overlay == nil {
		return &merged
	}
	if overlay.PromptTokens != 0 {
		merged.PromptTokens = overlay.PromptTokens
	}
	if overlay.CompletionTokens != 0 {
		merged.CompletionTokens = overlay.CompletionTokens
	}
	if overlay.CachedTokens != 0 {
		merged.CachedTokens = overlay.CachedTokens
	}
	if overlay.CacheCreationTokens != 0 {
		merged.CacheCreationTokens = overlay.CacheCreationTokens
	}
	if overlay.CacheCreation5mTokens != 0 {
		merged.CacheCreation5mTokens = overlay.CacheCreation5mTokens
	}
	if overlay.CacheCreation1hTokens != 0 {
		merged.CacheCreation1hTokens = overlay.CacheCreation1hTokens
	}
	if overlay.Calls != 0 {
		merged.Calls = overlay.Calls
	}
	if overlay.ImageSize != "" {
		merged.ImageSize = overlay.ImageSize
	}
	if overlay.ImageQuality != "" {
		merged.ImageQuality = overlay.ImageQuality
	}
	return &merged
}

func parseCodexImageUsage(raw json.RawMessage) (dto.Usage, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return dto.Usage{}, false
	}
	var wire struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		InputDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		CachedInputTokens int `json:"cached_input_tokens"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return dto.Usage{}, false
	}
	cached := wire.CachedInputTokens
	if cached == 0 {
		cached = wire.InputDetails.CachedTokens
	}
	return dto.Usage{PromptTokens: wire.InputTokens, CompletionTokens: wire.OutputTokens, CachedTokens: cached}, true
}

func isCodexImageEndpoint(endpoint string) bool {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case "images_generations", "images_edits":
		return true
	default:
		return false
	}
}
