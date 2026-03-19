package fastwire

import "testing"

func TestBaseHandlerNoPanic(t *testing.T) {
	var h BaseHandler
	// All methods should be no-ops without panicking.
	h.OnConnect(nil)
	h.OnDisconnect(nil, DisconnectGraceful)
	h.OnMessage(nil, []byte("test"), 0)
	h.OnError(nil, ErrConnectionClosed)
}

func TestBaseHandlerSatisfiesHandler(t *testing.T) {
	var _ Handler = BaseHandler{}
	var _ Handler = &BaseHandler{}
}
