package fastwire

// Handler defines callbacks for connection events.
// Implement this interface to receive notifications when connections are
// established, disconnected, when messages arrive, and when errors occur.
type Handler interface {
	// OnConnect is called when a new connection is established.
	OnConnect(conn *Connection)
	// OnDisconnect is called when a connection is closed, with the reason for closure.
	OnDisconnect(conn *Connection, reason DisconnectReason)
	// OnMessage is called when a complete message is received on the given channel.
	OnMessage(conn *Connection, data []byte, channel byte)
	// OnError is called when a non-fatal error occurs on a connection.
	OnError(conn *Connection, err error)
}

// BaseHandler provides no-op implementations of Handler for embedding.
// Embed BaseHandler in your struct to only implement the callbacks you need.
type BaseHandler struct{}

// OnConnect is a no-op implementation of Handler.OnConnect.
func (BaseHandler) OnConnect(*Connection) {}

// OnDisconnect is a no-op implementation of Handler.OnDisconnect.
func (BaseHandler) OnDisconnect(*Connection, DisconnectReason) {}

// OnMessage is a no-op implementation of Handler.OnMessage.
func (BaseHandler) OnMessage(*Connection, []byte, byte) {}

// OnError is a no-op implementation of Handler.OnError.
func (BaseHandler) OnError(*Connection, error) {}
