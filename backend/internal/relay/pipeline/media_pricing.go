package pipeline

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
)

// enrichImageBillingUsage 补齐生图计费所需的张数与档位。
// 优先级：适配器已提取的响应事实 > 响应体字段 > 请求参数；请求值仅在上游未回传时兜底。
// 账号路径（CPA）不会经过 openai adaptor 的响应解析，因此必须在计费收尾统一补齐。
func enrichImageBillingUsage(path string, req *dto.ChatRequest, responseBody []byte, usage dto.Usage) dto.Usage {
	if !strings.HasSuffix(path, "/images/generations") && !strings.HasSuffix(path, "/images/edits") {
		return usage
	}

	if len(responseBody) > 0 && (usage.Calls < 1 || usage.ImageSize == "" || usage.ImageQuality == "") {
		var response struct {
			Data       []json.RawMessage `json:"data"`
			Size       string            `json:"size"`
			Resolution string            `json:"resolution"`
			Quality    string            `json:"quality"`
		}
		if json.Unmarshal(responseBody, &response) == nil {
			if usage.Calls < 1 && len(response.Data) > 0 {
				usage.Calls = len(response.Data)
			}
			if usage.ImageSize == "" {
				usage.ImageSize = firstNonEmpty(response.Resolution, response.Size)
			}
			if usage.ImageQuality == "" {
				usage.ImageQuality = response.Quality
			}
		}
	}

	if usage.Calls < 1 {
		if n := requestInt(req, "n"); n > 0 {
			usage.Calls = n
		}
	}
	if usage.ImageSize == "" {
		usage.ImageSize = firstNonEmpty(requestString(req, "resolution"), requestString(req, "size"))
	}
	if usage.ImageQuality == "" {
		usage.ImageQuality = requestString(req, "quality")
	}
	return usage
}

func requestString(req *dto.ChatRequest, key string) string {
	if req == nil {
		return ""
	}
	raw, ok := req.Get(key)
	if !ok {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func requestInt(req *dto.ChatRequest, key string) int {
	if req == nil {
		return 0
	}
	raw, ok := req.Get(key)
	if !ok {
		return 0
	}
	var value int
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		value, _ = strconv.Atoi(strings.TrimSpace(text))
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
