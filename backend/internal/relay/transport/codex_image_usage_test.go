package transport

import (
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
)

func TestParseCodexImageResponseUsage(t *testing.T) {
	body := []byte(`{"created":1,"size":"1024x1536","quality":"high","data":[{"b64_json":"a"},{"b64_json":"b"}],"usage":{"input_tokens":12,"output_tokens":34,"input_tokens_details":{"cached_tokens":5}}}`)
	usage := parseCodexImageResponseUsage(body)
	if usage == nil {
		t.Fatal("expected image usage")
		return
	}
	if usage.PromptTokens != 12 || usage.CompletionTokens != 34 || usage.CachedTokens != 5 || usage.Calls != 2 || usage.ImageSize != "1024x1536" || usage.ImageQuality != "high" {
		t.Fatalf("usage = %+v", *usage)
	}
}

func TestCodexImageUsageObserverHandlesSplitSSEFrames(t *testing.T) {
	observer := newCodexImageUsageObserver()
	observer.Feed([]byte("event: image_generation.completed\ndata: {\"type\":\"image_generation.completed\",\"size\":\"1024x1024\",\"quality\":\"medium\",\"usage\":{"))
	observer.Feed([]byte("\"input_tokens\":7,\"output_tokens\":9}}\n\n"))
	observer.Finish()
	usage := observer.Usage()
	if usage == nil {
		t.Fatal("expected observed usage")
		return
	}
	if usage.PromptTokens != 7 || usage.CompletionTokens != 9 || usage.Calls != 1 || usage.ImageSize != "1024x1024" || usage.ImageQuality != "medium" {
		t.Fatalf("usage = %+v", *usage)
	}
}

func TestMergeCodexImageUsageRetainsNativeTokens(t *testing.T) {
	base := &dto.Usage{PromptTokens: 3, CompletionTokens: 4}
	overlay := &dto.Usage{Calls: 2, ImageSize: "auto", ImageQuality: "low"}
	merged := mergeCodexImageUsage(base, overlay)
	if merged == nil || merged.PromptTokens != 3 || merged.CompletionTokens != 4 || merged.Calls != 2 || merged.ImageSize != "auto" || merged.ImageQuality != "low" {
		t.Fatalf("merged = %+v", merged)
	}
}
