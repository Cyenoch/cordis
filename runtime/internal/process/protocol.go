package process

import (
	"context"

	"github.com/Cyenoch/cordis/contract"
)

// PluginSession preserves host-specific lifecycle operations through the process
// ownership wrapper. Nil means the endpoint uses the generic wire protocol.
func (s *Session) PluginSession() contract.PluginSession {
	protocol, ok := s.endpoint.(contract.PluginSession)
	if !ok {
		return nil
	}
	return processProtocol{process: s, PluginSession: protocol}
}

type processProtocol struct {
	contract.PluginSession
	process *Session
}

func (p processProtocol) Shutdown(ctx context.Context) error {
	p.process.expectedStop.Store(true)
	return p.PluginSession.Shutdown(ctx)
}
