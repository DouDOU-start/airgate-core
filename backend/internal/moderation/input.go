package moderation

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/DouDOU-start/airgate-core/internal/pkg/multipartform"
)

// Input 抽取出的待审核输入：文本 + 图片（data URL 或 http(s) URL）。
type Input struct {
	Text   string
	Images []string
}

// Normalize 压缩空白、截断超长文本、图片去重。
func (in *Input) Normalize() {
	if in == nil {
		return
	}
	in.Text = trimRunes(normalizeText(in.Text), maxInputRunes)
	in.Images = normalizeImages(in.Images)
}

// IsEmpty 无文本且无图片。
func (in Input) IsEmpty() bool {
	return strings.TrimSpace(in.Text) == "" && len(in.Images) == 0
}

// ModerationInput 转成外部审核 API 的 input 载荷：纯文本或 text+image_url parts
// （图片最多 1 张，多图随机取 1 控制成本）。
func (in Input) ModerationInput() any {
	images := limitImages(in.Images)
	if len(images) == 0 {
		return in.Text
	}
	parts := make([]apiInputPart, 0, len(images)+1)
	if strings.TrimSpace(in.Text) != "" {
		parts = append(parts, apiInputPart{Type: "text", Text: in.Text})
	}
	for _, image := range images {
		parts = append(parts, apiInputPart{Type: "image_url", ImageURL: &apiImageURLRef{URL: image}})
	}
	return parts
}

// Hash 输入内容 sha256（文本 + 每张图的哈希），作为命中哈希缓存与稳定采样的键。
func (in Input) Hash() string {
	h := sha256.New()
	_, _ = h.Write([]byte("text:"))
	_, _ = h.Write([]byte(in.Text))
	for _, image := range in.Images {
		imageHash := sha256.Sum256([]byte(image))
		_, _ = h.Write([]byte("\nimage:"))
		_, _ = h.Write([]byte(hex.EncodeToString(imageHash[:])))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ExtractInput 按协议从请求体抽取「最后一条用户输入」的文本与图片。
// contentType 仅 openai_video 的 multipart 提交体需要，JSON 协议传空即可。
// 审核对象刻意只取最后一条 user 消息：历史消息在此前请求中已审过。
func ExtractInput(protocol, contentType string, body []byte) Input {
	if protocol == ProtocolOpenAIVideo && strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "multipart/") {
		return extractMultipartPrompt(contentType, body)
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return Input{}
	}
	var parts []string
	var images []string
	switch protocol {
	case ProtocolAnthropicMessages:
		collectLastAnthropicUserMessage(gjson.GetBytes(body, "messages"), &parts, &images)
	case ProtocolOpenAIChat:
		collectLastRoleMessage(gjson.GetBytes(body, "messages"), "user", &parts, &images)
	case ProtocolOpenAIResponses:
		collectLastResponsesInput(gjson.GetBytes(body, "input"), &parts, &images)
	case ProtocolGemini:
		collectLastGeminiContent(gjson.GetBytes(body, "contents"), &parts, &images)
	case ProtocolOpenAIImages, ProtocolOpenAIVideo:
		addText(&parts, gjson.GetBytes(body, "prompt").String())
	case ProtocolSuno:
		// suno 提交体的文本字段（music：prompt 歌词 / gpt_description_prompt 描述 /
		// tags 风格 / title 标题；lyrics：prompt 描述）。
		for _, field := range []string{"prompt", "gpt_description_prompt", "lyrics", "title", "tags"} {
			addText(&parts, gjson.GetBytes(body, field).String())
		}
	default:
		collectLastResponsesInput(gjson.GetBytes(body, "input"), &parts, &images)
		collectLastRoleMessage(gjson.GetBytes(body, "messages"), "user", &parts, &images)
		collectLastGeminiContent(gjson.GetBytes(body, "contents"), &parts, &images)
	}
	out := Input{
		Text:   normalizeText(strings.Join(parts, "\n")),
		Images: normalizeImages(images),
	}
	out.Normalize()
	return out
}

func extractMultipartPrompt(contentType string, body []byte) Input {
	fields, err := multipartform.ExtractFields(body, contentType, "prompt")
	if err != nil {
		return Input{}
	}
	out := Input{Text: normalizeText(fields["prompt"])}
	out.Normalize()
	return out
}

func collectLastRoleMessage(messages gjson.Result, role string, parts *[]string, images *[]string) {
	if !messages.IsArray() {
		return
	}
	array := messages.Array()
	if len(array) == 0 {
		return
	}
	last := array[len(array)-1]
	if strings.ToLower(strings.TrimSpace(last.Get("role").String())) != role {
		return
	}
	var candidate []string
	var candidateImages []string
	collectContentValue(last.Get("content"), &candidate, &candidateImages)
	if normalizeText(strings.Join(candidate, "\n")) == "" && len(candidateImages) == 0 {
		return
	}
	*parts = append(*parts, candidate...)
	*images = append(*images, candidateImages...)
}

func collectLastAnthropicUserMessage(messages gjson.Result, parts *[]string, images *[]string) {
	if !messages.IsArray() {
		return
	}
	array := messages.Array()
	if len(array) == 0 {
		return
	}
	last := array[len(array)-1]
	if strings.ToLower(strings.TrimSpace(last.Get("role").String())) != "user" {
		return
	}
	var candidate []string
	var candidateImages []string
	collectAnthropicUserContentValue(last.Get("content"), &candidate, &candidateImages)
	if normalizeText(strings.Join(candidate, "\n")) == "" && len(candidateImages) == 0 {
		return
	}
	*parts = append(*parts, candidate...)
	*images = append(*images, candidateImages...)
}

func collectAnthropicUserContentValue(value gjson.Result, parts *[]string, images *[]string) {
	switch {
	case !value.Exists():
		return
	case value.Type == gjson.String:
		if !isSystemReminderText(value.String()) {
			addText(parts, value.String())
		}
	case value.IsArray():
		value.ForEach(func(_, item gjson.Result) bool {
			collectAnthropicUserContentValue(item, parts, images)
			return true
		})
	case value.IsObject():
		typ := strings.ToLower(strings.TrimSpace(value.Get("type").String()))
		switch typ {
		case "", "text", "input_text", "message":
			if value.Get("text").Exists() && !isSystemReminderText(value.Get("text").String()) {
				addText(parts, value.Get("text").String())
			}
			if value.Get("content").Exists() {
				collectAnthropicUserContentValue(value.Get("content"), parts, images)
			}
		case "image_url", "input_image", "image":
			collectContentValue(value, parts, images)
		}
	}
}

// isSystemReminderText 客户端注入的 system-reminder 块不是用户创作内容，剔除。
func isSystemReminderText(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "<system-reminder>")
}

func collectLastResponsesInput(input gjson.Result, parts *[]string, images *[]string) {
	switch {
	case !input.Exists():
		return
	case input.Type == gjson.String:
		addText(parts, input.String())
	case input.IsArray():
		array := input.Array()
		if len(array) == 0 {
			return
		}
		last := array[len(array)-1]
		if !isResponsesUserTextItem(last) {
			return
		}
		collectContentValue(last.Get("content"), parts, images)
		if last.Get("type").String() == "input_text" || last.Get("text").Exists() {
			collectContentValue(last, parts, images)
		}
	case input.IsObject():
		if isResponsesUserTextItem(input) {
			collectContentValue(input.Get("content"), parts, images)
			if input.Get("type").String() == "input_text" || input.Get("text").Exists() {
				collectContentValue(input, parts, images)
			}
		}
	}
}

func isResponsesUserTextItem(item gjson.Result) bool {
	role := strings.ToLower(strings.TrimSpace(item.Get("role").String()))
	if role == "user" {
		return responsesItemHasText(item)
	}
	if role != "" {
		return false
	}
	return responsesItemHasText(item)
}

func responsesItemHasText(item gjson.Result) bool {
	var parts []string
	var images []string
	collectContentValue(item.Get("content"), &parts, &images)
	if item.Get("type").String() == "input_text" || item.Get("text").Exists() {
		collectContentValue(item, &parts, &images)
	}
	return normalizeText(strings.Join(parts, "\n")) != "" || len(images) > 0
}

func collectLastGeminiContent(contents gjson.Result, parts *[]string, images *[]string) {
	if !contents.IsArray() {
		return
	}
	array := contents.Array()
	if len(array) == 0 {
		return
	}
	last := array[len(array)-1]
	role := strings.ToLower(strings.TrimSpace(last.Get("role").String()))
	if role != "" && role != "user" {
		return
	}
	var candidate []string
	var candidateImages []string
	if arr := last.Get("parts"); arr.IsArray() {
		arr.ForEach(func(_, part gjson.Result) bool {
			addText(&candidate, part.Get("text").String())
			addGeminiImage(&candidateImages, part)
			return true
		})
	}
	if normalizeText(strings.Join(candidate, "\n")) == "" && len(candidateImages) == 0 {
		return
	}
	*parts = append(*parts, candidate...)
	*images = append(*images, candidateImages...)
}

func collectContentValue(value gjson.Result, parts *[]string, images *[]string) {
	switch {
	case !value.Exists():
		return
	case value.Type == gjson.String:
		addText(parts, value.String())
	case value.IsArray():
		value.ForEach(func(_, item gjson.Result) bool {
			collectContentValue(item, parts, images)
			return true
		})
	case value.IsObject():
		typ := strings.ToLower(strings.TrimSpace(value.Get("type").String()))
		addImage(images, value.Get("image_url.url").String())
		addImage(images, value.Get("image_url").String())
		addImage(images, value.Get("url").String())
		addImageData(images, value.Get("source.media_type").String(), value.Get("source.data").String())
		addImageData(images, value.Get("source.mediaType").String(), value.Get("source.data").String())
		addImageData(images, value.Get("media_type").String(), value.Get("data").String())
		addImageData(images, value.Get("mime_type").String(), value.Get("data").String())
		addImageData(images, value.Get("mimeType").String(), value.Get("data").String())
		addImage(images, value.Get("source.data").String())
		addImage(images, value.Get("data").String())
		addImage(images, value.Get("base64").String())
		switch typ {
		case "", "text", "input_text", "message":
			if value.Get("text").Exists() {
				addText(parts, value.Get("text").String())
			}
			if value.Get("content").Exists() {
				collectContentValue(value.Get("content"), parts, images)
			}
		case "image_url", "input_image", "image":
		}
	}
}

func addGeminiImage(images *[]string, part gjson.Result) {
	if inlineData := part.Get("inline_data"); inlineData.IsObject() {
		mimeType := strings.TrimSpace(inlineData.Get("mime_type").String())
		data := strings.TrimSpace(inlineData.Get("data").String())
		if mimeType != "" && data != "" {
			addImage(images, fmt.Sprintf("data:%s;base64,%s", mimeType, data))
		}
	}
	if inlineData := part.Get("inlineData"); inlineData.IsObject() {
		mimeType := strings.TrimSpace(inlineData.Get("mimeType").String())
		data := strings.TrimSpace(inlineData.Get("data").String())
		if mimeType != "" && data != "" {
			addImage(images, fmt.Sprintf("data:%s;base64,%s", mimeType, data))
		}
	}
	addImage(images, part.Get("file_data.file_uri").String())
	addImage(images, part.Get("fileData.fileUri").String())
}

func addImageData(images *[]string, mimeType string, data string) {
	mimeType = strings.TrimSpace(mimeType)
	data = strings.TrimSpace(data)
	if mimeType == "" || data == "" {
		return
	}
	addImage(images, fmt.Sprintf("data:%s;base64,%s", mimeType, data))
}

func addImage(images *[]string, image string) {
	image = strings.TrimSpace(image)
	if image == "" {
		return
	}
	if strings.HasPrefix(image, "data:") || strings.HasPrefix(image, "http://") || strings.HasPrefix(image, "https://") {
		*images = append(*images, image)
	}
}

func normalizeImages(images []string) []string {
	out := make([]string, 0, len(images))
	seen := make(map[string]struct{}, len(images))
	for _, image := range images {
		image = strings.TrimSpace(image)
		if image == "" {
			continue
		}
		if _, ok := seen[image]; ok {
			continue
		}
		seen[image] = struct{}{}
		out = append(out, image)
	}
	return out
}

func limitImages(images []string) []string {
	if len(images) <= maxInputImages {
		return images
	}
	idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(images))))
	if err != nil {
		return images[:maxInputImages]
	}
	return []string{images[int(idx.Int64())]}
}

func addText(parts *[]string, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if strings.Contains(text, "<system-reminder>") {
		return
	}
	*parts = append(*parts, text)
}

func normalizeText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}
