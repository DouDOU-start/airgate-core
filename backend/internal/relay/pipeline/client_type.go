package pipeline

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/clientid"
)

// relayClientType returns the normalized client identity used by routing,
// hooks, native transports, auditing, and allowed-client checks. Header-based
// detection is shared with clientid; finite Codex route aliases are added only
// at the pipeline boundary where those paths are meaningful.
func relayClientType(c *gin.Context) string {
	if c == nil {
		return ""
	}
	clientType := clientid.Get(c)
	if clientType != "" {
		return clientType
	}
	if c.Request != nil {
		// Preserve clientid's Claude-over-Codex precedence even when this helper
		// is called directly before Detect has populated the Gin context.
		if classified := clientid.ClassifyRequest(c.Request); classified != "" {
			return classified
		}
	}
	if fullPath := strings.ToLower(strings.TrimSpace(c.FullPath())); isDedicatedCodexRoutePath(fullPath) {
		return clientid.Codex
	}
	if c.Request != nil && c.Request.URL != nil && isDedicatedCodexRoutePath(c.Request.URL.Path) {
		return clientid.Codex
	}
	return ""
}
