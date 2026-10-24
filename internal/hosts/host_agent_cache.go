package hosts

import (
	"sync"
	"time"
)

// hostAgentInspectTTL is how long a verified, ready inspection result is
// reused by the proxy. The SSH connection and its pinned-fingerprint check
// still happen on every request; only the SFTP walk and the remote sha256sum of
// the helper binary are skipped. Install and status always inspect in full.
var hostAgentInspectTTL = 20 * time.Second

type hostAgentCacheEntry struct {
	digest  string // Identity.Digest: host id, endpoint, user, fingerprint, revision
	ready   HostAgentReady
	status  hostAgentStatus
	token   []byte
	expires time.Time
}

// hostAgentInspectCache memoizes ready host-agent inspections per host, keyed by
// the SSH identity digest so any revision, endpoint or fingerprint change misses.
type hostAgentInspectCache struct {
	mu      sync.Mutex
	entries map[string]*hostAgentCacheEntry
}

func (c *hostAgentInspectCache) get(identity Identity) (remoteHostAgent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[identity.HostID]
	if entry == nil {
		return remoteHostAgent{}, false
	}
	if entry.digest != identity.Digest || time.Now().After(entry.expires) {
		c.dropLocked(identity.HostID)
		return remoteHostAgent{}, false
	}
	return remoteHostAgent{status: entry.status, ready: entry.ready, token: append([]byte(nil), entry.token...), exists: true}, true
}

// put stores a ready inspection; anything else invalidates the host's entry.
func (c *hostAgentInspectCache) put(identity Identity, state remoteHostAgent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dropLocked(identity.HostID)
	if !state.status.Ready || !validHostAgentToken(state.token) {
		return
	}
	if c.entries == nil {
		c.entries = map[string]*hostAgentCacheEntry{}
	}
	c.entries[identity.HostID] = &hostAgentCacheEntry{
		digest: identity.Digest, ready: state.ready, status: state.status,
		token: append([]byte(nil), state.token...), expires: time.Now().Add(hostAgentInspectTTL),
	}
}

func (c *hostAgentInspectCache) invalidate(hostID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dropLocked(hostID)
}

func (c *hostAgentInspectCache) dropLocked(hostID string) {
	if entry := c.entries[hostID]; entry != nil {
		clear(entry.token)
		delete(c.entries, hostID)
	}
}
