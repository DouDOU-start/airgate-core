// Package multipartform 提供 multipart/form-data 原始体的只读字段提取：
// 不消费、不改动原始字节（上游收到的仍是原样体），仅用于路由/调度取值。
// 由同步转发（images edits）与异步任务（视频提交）两条链路共用。
package multipartform

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"strings"
)

// MaxFieldBytes 提取的普通字段值上限（model/stream/seconds 均为短值）。
const MaxFieldBytes = 1 << 10

// ExtractFields 从 multipart/form-data 原始体中提取指定名字的普通表单字段值
// （文件 part 一律跳过、不缓冲）。仅用于取值，原始字节不被改动。
func ExtractFields(body []byte, contentType string, names ...string) (map[string]string, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return nil, errors.New("Content-Type 必须是 multipart/form-data")
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, errors.New("multipart 缺少 boundary")
	}

	want := make(map[string]struct{}, len(names))
	for _, n := range names {
		want[n] = struct{}{}
	}
	got := make(map[string]string, len(names))
	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	for len(got) < len(want) {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		// 非目标字段或文件 part：跳过（NextPart 会自动丢弃上一 part 的剩余内容）。
		if _, ok := want[part.FormName()]; !ok || part.FileName() != "" {
			continue
		}
		value, err := io.ReadAll(io.LimitReader(part, MaxFieldBytes))
		if err != nil {
			return nil, err
		}
		got[part.FormName()] = string(value)
	}
	return got, nil
}
