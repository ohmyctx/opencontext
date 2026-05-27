// Package config re-exports subscription.Config for convenient daemon wiring.
// It also adds HTTP query handler logic that lives at the daemon level.
package config

import "github.com/yetanotherai/opencontext/internal/subscription"

// Config is the full daemon configuration.
type Config = subscription.Config

// Load reads configuration from path (or default locations if empty).
var Load = subscription.Load

// DefaultConfig returns default configuration.
var DefaultConfig = subscription.DefaultConfig
