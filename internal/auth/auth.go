// Package auth validates proxy tokens before forwarding.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/denisitpro/lux-l7/internal/config"
)

// Validator checks X-L7-Proxy-Token when auth is enabled.
type Validator struct {
	enabled        bool
	mode           config.AuthMode
	instanceHashes [][sha256.Size]byte
	hostHashes     map[string][sha256.Size]byte
}

// New builds a validator from process configuration.
func New(cfg config.Config) (*Validator, error) {
	v := &Validator{
		enabled: cfg.AuthEnabled,
		mode:    cfg.AuthMode,
	}
	if !cfg.AuthEnabled {
		return v, nil
	}
	switch cfg.AuthMode {
	case config.AuthModeInstance:
		v.instanceHashes = hashTokens(cfg.InstanceToken)
		if len(v.instanceHashes) == 0 {
			return nil, fmt.Errorf("instance token missing")
		}
	case config.AuthModePerHost:
		if cfg.HostTokenJSON == "" {
			return nil, fmt.Errorf("host token map missing")
		}
		var raw map[string]string
		if err := json.Unmarshal([]byte(cfg.HostTokenJSON), &raw); err != nil {
			return nil, fmt.Errorf("PROXY_AUTH_HOST_MAP: %w", err)
		}
		v.hostHashes = make(map[string][sha256.Size]byte, len(raw))
		for host, token := range raw {
			host = mapHostKey(host)
			if host == "" || token == "" {
				continue
			}
			v.hostHashes[host] = sha256.Sum256([]byte(token))
		}
		if len(v.hostHashes) == 0 {
			return nil, fmt.Errorf("PROXY_AUTH_HOST_MAP has no valid entries")
		}
	default:
		return nil, fmt.Errorf("unknown auth mode %q", cfg.AuthMode)
	}
	return v, nil
}

// hashTokens splits a comma-separated token list (whitespace trimmed, empty
// items dropped, duplicates collapsed) and returns the SHA-256 of each token.
func hashTokens(raw string) [][sha256.Size]byte {
	var out [][sha256.Size]byte
	seen := map[[sha256.Size]byte]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		h := sha256.Sum256([]byte(part))
		if _, dup := seen[h]; dup {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	return out
}

// InstanceTokenCount reports how many instance tokens are configured.
func (v *Validator) InstanceTokenCount() int {
	if v == nil {
		return 0
	}
	return len(v.instanceHashes)
}

// Enabled reports whether tokens are required.
func (v *Validator) Enabled() bool {
	return v != nil && v.enabled
}

// Authorize returns true when the request may proceed.
func (v *Validator) Authorize(token, targetAuthority string) bool {
	if v == nil || !v.enabled {
		return true
	}
	if token == "" {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	switch v.mode {
	case config.AuthModeInstance:
		// Compare against every hash and never exit early, so timing does not
		// reveal which entry matched.
		ok := 0
		for i := range v.instanceHashes {
			ok |= subtle.ConstantTimeCompare(sum[:], v.instanceHashes[i][:])
		}
		return ok == 1
	case config.AuthModePerHost:
		host := mapHostKey(targetAuthority)
		want, ok := v.hostHashes[host]
		if !ok {
			return false
		}
		return subtle.ConstantTimeCompare(sum[:], want[:]) == 1
	default:
		return false
	}
}

func mapHostKey(authority string) string {
	authority = strings.ToLower(strings.TrimSpace(authority))
	if authority == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(authority)
	if err != nil {
		return authority
	}
	return host
}
