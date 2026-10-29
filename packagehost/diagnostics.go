package packagehost

import (
	"context"
	"log/slog"

	"github.com/Cyenoch/cordis/contract"
)

func (s *Store) logMutation(ctx context.Context, operation string, ref contract.PackageRef, entry string, committed bool, err error) {
	if s.logger == nil {
		return
	}
	outcome, level := "unchanged", slog.LevelInfo
	if committed {
		outcome = "committed"
	}
	attrs := []slog.Attr{
		slog.String("operation", operation), slog.String("package", ref.ID),
		slog.String("digest", ref.Digest), slog.String("entry", entry),
	}
	if err != nil {
		level = slog.LevelError
		outcome = "rejected"
		if committed {
			outcome = "committed_error"
		}
		attrs = append(attrs, slog.String("error", err.Error()), slog.String("errorCode", string(contract.ErrorCode(err))))
	}
	attrs = append(attrs, slog.String("outcome", outcome))
	s.logger.LogAttrs(ctx, level, "package operation", attrs...)
}
