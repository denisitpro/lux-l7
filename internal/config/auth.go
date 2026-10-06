package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// authEnv is the raw auth-related environment, before it is resolved into Config.
type authEnv struct {
	enabledSet bool // AUTH_ENABLED present
	enabled    bool // its value when present
	modeSet    bool // AUTH_MODE present
}

// envBoolOpt is envBool without a fallback: the second result reports whether the
// variable was set at all, so "unset" stays distinguishable from "false".
func envBoolOpt(key string) (value, set bool, err error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return false, false, nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return false, false, fmt.Errorf("%s: invalid boolean %q (use true or false)", key, raw)
	}
	return b, true, nil
}

// resolveAuth fills cfg.AuthEnabled and cfg.AuthMode. Auth follows the tokens:
// on exactly when PROXY_AUTH_TOKEN or PROXY_AUTH_HOST_MAP is set, mode follows from
// which one. AUTH_ENABLED and AUTH_MODE are optional and only checked for consistency.
func resolveAuth(cfg *Config, env authEnv) error {
	switch cfg.AuthMode {
	case AuthModeInstance, AuthModePerHost:
	default:
		return fmt.Errorf("AUTH_MODE must be instance or per_host, got %q", cfg.AuthMode)
	}
	explicitOn := env.enabledSet && env.enabled

	if cfg.ProxyMode == ProxyModeRoute {
		// Route mode has no auth; tokens are ignored unless auth is forced on.
		if explicitOn {
			return fmt.Errorf("auth is not supported in route mode: unset AUTH_ENABLED")
		}
		cfg.AuthEnabled = false
		return nil
	}

	hasInstance := cfg.InstanceToken != ""
	hasHostMap := cfg.HostTokenJSON != ""
	if hasInstance && hasHostMap {
		return fmt.Errorf("PROXY_AUTH_TOKEN and PROXY_AUTH_HOST_MAP are both set: choose one")
	}
	hasTokens := hasInstance || hasHostMap

	if env.enabledSet && !env.enabled {
		if hasTokens {
			return fmt.Errorf("AUTH_ENABLED=false contradicts PROXY_AUTH_TOKEN / PROXY_AUTH_HOST_MAP: unset the tokens or AUTH_ENABLED")
		}
		cfg.AuthEnabled = false
		return nil
	}
	if !hasTokens {
		if explicitOn {
			return fmt.Errorf("AUTH_ENABLED=true requires PROXY_AUTH_TOKEN or PROXY_AUTH_HOST_MAP")
		}
		cfg.AuthEnabled = false
		return nil
	}

	inferred := AuthModeInstance
	if hasHostMap {
		inferred = AuthModePerHost
	}
	if env.modeSet && cfg.AuthMode != inferred {
		if inferred == AuthModeInstance {
			return fmt.Errorf("AUTH_MODE=%s requires PROXY_AUTH_HOST_MAP, but only PROXY_AUTH_TOKEN is set", cfg.AuthMode)
		}
		return fmt.Errorf("AUTH_MODE=%s requires PROXY_AUTH_TOKEN, but only PROXY_AUTH_HOST_MAP is set", cfg.AuthMode)
	}
	cfg.AuthMode = inferred
	cfg.AuthEnabled = true
	return nil
}
