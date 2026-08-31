package middleware

import "strings"

// sensitivePathPrefixes contains local bearer-token endpoints whose final
// path segment is itself a credential.  Keep this list in middleware rather
// than in the relay package so every ordinary log site can redact the token
// without importing (and cyclically depending on) the pipeline.
var sensitivePathPrefixes = [...]string{
	"/_airgate/codex/files/upload/",
	"/_airgate/codex/plugins/upload/",
}

// RedactSensitiveRequestPath returns a log-safe representation of an inbound
// request path.  The opaque upload token is intentionally never emitted to
// ordinary access/rate-limit/recovery logs: anyone who can read those logs
// would otherwise be able to replay the unauthenticated PUT.  Routing still
// uses the original URL path; this helper is only for diagnostics.
func RedactSensitiveRequestPath(path string) string {
	for _, prefix := range sensitivePathPrefixes {
		if strings.HasPrefix(path, prefix) && len(path) > len(prefix) {
			return prefix + "[REDACTED]"
		}
	}
	return path
}
