package cpa

import (
	"bytes"
	"encoding/json"
)

// responsesSSEFramer 将 CPA executor 的任意 chunk 边界恢复为完整的 SSE 事件。
// xAI executor 可能分别返回 event 行、data 行和空行，也可能从 JSON 中间拆分 chunk。
type responsesSSEFramer struct {
	pending []byte
}

// WriteChunk 接收一个上游 chunk，返回当前已经可以安全下发的完整 SSE 事件。
func (f *responsesSSEFramer) WriteChunk(chunk []byte) [][]byte {
	if len(chunk) == 0 {
		return nil
	}
	if responsesSSENeedsLineBreak(f.pending, chunk) {
		f.pending = append(f.pending, '\n')
	}
	f.pending = append(f.pending, chunk...)

	var frames [][]byte
	for {
		frameLen := responsesSSEFrameLen(f.pending)
		if frameLen == 0 {
			break
		}
		frames = append(frames, normalizeResponsesSSEFrame(f.pending[:frameLen]))
		f.pending = append(f.pending[:0], f.pending[frameLen:]...)
	}

	if len(bytes.TrimSpace(f.pending)) == 0 {
		f.pending = f.pending[:0]
		return frames
	}
	if responsesSSECanEmitWithoutDelimiter(f.pending) {
		frames = append(frames, normalizeResponsesSSEFrame(f.pending))
		f.pending = f.pending[:0]
	}
	return frames
}

// Flush 在上游正常结束时下发最后一个完整事件，并丢弃不完整的尾部数据。
func (f *responsesSSEFramer) Flush() [][]byte {
	if len(f.pending) == 0 {
		return nil
	}
	defer func() { f.pending = f.pending[:0] }()
	if len(bytes.TrimSpace(f.pending)) == 0 || !responsesSSECanEmitWithoutDelimiter(f.pending) {
		return nil
	}
	return [][]byte{normalizeResponsesSSEFrame(f.pending)}
}

func normalizeResponsesSSEFrame(frame []byte) []byte {
	out := append([]byte(nil), frame...)
	if bytes.HasSuffix(out, []byte("\n\n")) || bytes.HasSuffix(out, []byte("\r\n\r\n")) {
		return out
	}
	if bytes.HasSuffix(out, []byte("\r\n")) {
		return append(out, '\r', '\n')
	}
	if bytes.HasSuffix(out, []byte("\n")) {
		return append(out, '\n')
	}
	return append(out, '\n', '\n')
}

func responsesSSEFrameLen(chunk []byte) int {
	if len(chunk) == 0 {
		return 0
	}
	lf := bytes.Index(chunk, []byte("\n\n"))
	crlf := bytes.Index(chunk, []byte("\r\n\r\n"))
	switch {
	case lf < 0:
		if crlf < 0 {
			return 0
		}
		return crlf + 4
	case crlf < 0:
		return lf + 2
	case lf < crlf:
		return lf + 2
	default:
		return crlf + 4
	}
}

func responsesSSECanEmitWithoutDelimiter(chunk []byte) bool {
	trimmed := bytes.TrimSpace(chunk)
	if len(trimmed) == 0 || !responsesSSEHasField(trimmed, []byte("data:")) {
		return false
	}
	return responsesSSEDataLinesValid(trimmed)
}

func responsesSSEHasField(chunk, prefix []byte) bool {
	for len(chunk) > 0 {
		line := chunk
		if i := bytes.IndexByte(chunk, '\n'); i >= 0 {
			line = chunk[:i]
			chunk = chunk[i+1:]
		} else {
			chunk = nil
		}
		if bytes.HasPrefix(bytes.TrimSpace(line), prefix) {
			return true
		}
	}
	return false
}

func responsesSSEDataLinesValid(chunk []byte) bool {
	for len(chunk) > 0 {
		line := chunk
		if i := bytes.IndexByte(chunk, '\n'); i >= 0 {
			line = chunk[:i]
			chunk = chunk[i+1:]
		} else {
			chunk = nil
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 || !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[len("data:"):])
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		if !json.Valid(data) {
			return false
		}
	}
	return true
}

func responsesSSENeedsLineBreak(pending, chunk []byte) bool {
	if len(pending) == 0 || len(chunk) == 0 {
		return false
	}
	if bytes.HasSuffix(pending, []byte("\n")) || bytes.HasSuffix(pending, []byte("\r")) {
		return false
	}
	if chunk[0] == '\n' || chunk[0] == '\r' {
		return false
	}
	trimmed := bytes.TrimLeft(chunk, " \t")
	for _, prefix := range [][]byte{
		[]byte("data:"), []byte("event:"), []byte("id:"), []byte("retry:"), []byte(":"),
	} {
		if bytes.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}
