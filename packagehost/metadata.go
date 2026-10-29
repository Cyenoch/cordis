package packagehost

import "log/slog"

type Options struct {
	Trust  TrustPolicy
	Limits Limits
	// Logger is called synchronously outside the Store lock. Handlers must
	// return promptly and must not reenter mutation or shutdown operations.
	Logger *slog.Logger
}
