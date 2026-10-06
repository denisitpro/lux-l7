package config

import "fmt"

// removedAuthVars are former settings that are no longer read. They only trigger a
// warning, and their values are never parsed.
var removedAuthVars = []string{"AUTH_ENABLED", "AUTH_MODE"}

// resolveAuth fills cfg.AuthEnabled and cfg.AuthMode. Auth follows the tokens: on
// exactly when PROXY_AUTH_TOKEN or PROXY_AUTH_HOST_MAP is set, and the variable that
// is set picks the mode.
func resolveAuth(cfg *Config) error {
	hasInstance := cfg.InstanceToken != ""
	hasHostMap := cfg.HostTokenJSON != ""

	if cfg.ProxyMode == ProxyModeRoute {
		// Route mode has no auth: a token would make an operator believe it is protected.
		if hasInstance || hasHostMap {
			return fmt.Errorf("auth is not supported in route mode: unset PROXY_AUTH_TOKEN / PROXY_AUTH_HOST_MAP")
		}
		return nil
	}
	if hasInstance && hasHostMap {
		return fmt.Errorf("PROXY_AUTH_TOKEN and PROXY_AUTH_HOST_MAP are both set: choose one")
	}
	switch {
	case hasInstance:
		cfg.AuthEnabled, cfg.AuthMode = true, AuthModeInstance
	case hasHostMap:
		cfg.AuthEnabled, cfg.AuthMode = true, AuthModePerHost
	}
	return nil
}
