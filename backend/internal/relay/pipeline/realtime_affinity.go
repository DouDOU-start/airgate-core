package pipeline

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
)

func realtimeCallAffinityKey(userID, groupID int, callID string) string {
	return fmt.Sprintf("realtime-call|%d|%d|%s", userID, groupID, callID)
}

func (p *Pipeline) rememberRealtimeCallAccount(userID, groupID int, location string, accountID int) {
	callID := realtimeCallIDFromLocation(location)
	if p == nil || callID == "" || accountID <= 0 {
		return
	}
	p.sessionAffinity.bind(realtimeCallAffinityKey(userID, groupID, callID), routeAccount, accountID)
}

// realtimeCallAccount returns the account used to create callID. The boolean
// reports whether an affinity binding existed, even when its account has since
// become invalid; callers must fail closed in that case instead of joining the
// account-scoped call with an unrelated credential.
func (p *Pipeline) realtimeCallAccount(userID, groupID int, callID string) (*accountreg.Snapshot, bool) {
	if p == nil || p.accounts == nil || strings.TrimSpace(callID) == "" {
		return nil, false
	}
	kind, accountID, ok := p.sessionAffinity.lookup(realtimeCallAffinityKey(userID, groupID, strings.TrimSpace(callID)))
	if !ok || kind != routeAccount {
		return nil, false
	}
	account, ok := p.accounts.Snapshot(accountID)
	if !ok || account == nil || account.State == accountreg.StateDisabled || !isNativeCodexAccount(account) {
		return nil, true
	}
	if _, allowed := account.GroupIDs[groupID]; !allowed {
		return nil, true
	}
	return account, true
}

func realtimeCallIDFromLocation(location string) string {
	parsed, err := url.Parse(strings.TrimSpace(location))
	if err != nil {
		return ""
	}
	// URL.Path is already unescaped, so an encoded slash such as %2F would be
	// mistaken for a real segment boundary. Split EscapedPath first and decode
	// exactly one segment. The official Frameless client treats call IDs as
	// opaque and supports separators inside that escaped segment; preserving the
	// decoded value here keeps account affinity identical to the later sideband
	// request, which re-escapes it before forwarding.
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	// The identifier must be the final path segment. Looking farther back would
	// accidentally accept malformed locations such as `/live/id/extra` and
	// bind the earlier `id` to the account.
	i := len(segments) - 1
	if i < 2 {
		return ""
	}
	segment, ok := realtimeLocationCallIDSegment(segments[i])
	if !ok {
		return ""
	}
	previous, ok := realtimeLocationRouteSegment(segments[i-1])
	if !ok {
		return ""
	}
	if strings.EqualFold(previous, "live") {
		return segment
	}
	if strings.EqualFold(previous, "calls") {
		beforePrevious, ok := realtimeLocationRouteSegment(segments[i-2])
		if ok && (strings.EqualFold(beforePrevious, "realtime") || strings.EqualFold(beforePrevious, "calls")) {
			return segment
		}
	}
	return ""
}

func realtimeLocationCallIDSegment(raw string) (string, bool) {
	segment, err := url.PathUnescape(raw)
	if err != nil {
		return "", false
	}
	if err := validateRealtimeCallID(segment); err != nil {
		return "", false
	}
	return segment, true
}

func realtimeLocationRouteSegment(raw string) (string, bool) {
	segment, err := url.PathUnescape(raw)
	if err != nil || strings.ContainsAny(segment, "/\\%?#") {
		return "", false
	}
	return segment, true
}
