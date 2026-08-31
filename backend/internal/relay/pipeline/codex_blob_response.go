package pipeline

import (
	"io"
	"net/http"
)

// copySafeCodexBlobResponseHeaders projects storage-service response metadata
// back to the Codex client without reflecting URL-bearing redirect metadata.
// A signed upload URL is a bearer credential; storage services normally return
// an empty body and an ETag, but a proxy or a compromised endpoint could place
// the URL in Location/Content-Location/Refresh/Link.  Those fields are not
// needed by the official PUT contract and are therefore deliberately removed.
func copySafeCodexBlobResponseHeaders(dst, src http.Header) {
	copySafeUpstreamResponseHeaders(dst, src)
	for _, name := range []string{"Location", "Content-Location", "Refresh", "Link"} {
		dst.Del(name)
	}
}

// drainCodexBlobResponseBody consumes a bounded amount of the storage response
// and intentionally does not expose it to the caller.  Blob PUT responses are
// bodyless in the official contract; suppressing an arbitrary body prevents a
// storage endpoint from echoing the signed URL (or other provider credentials)
// into the client's response.  The body remains bounded so a malicious
// endpoint cannot turn an error response into an unbounded memory/read loop.
func drainCodexBlobResponseBody(body io.Reader) error {
	if body == nil {
		return nil
	}
	_, err := io.Copy(io.Discard, io.LimitReader(body, 1<<20))
	return err
}
