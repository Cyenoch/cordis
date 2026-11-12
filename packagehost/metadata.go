package packagehost

import "log/slog"

// Options configures a Store. Limits defaults to DefaultLimits.
type Options struct {
	Trust  TrustPolicy
	Limits Limits
	// Logger is called synchronously outside the Store lock. Handlers must
	// return promptly and must not reenter mutation or shutdown operations.
	Logger *slog.Logger
}
