package fastwire

import "sync"

// MigrationToken is an 8-byte connection identifier for connection migration.
type MigrationToken [8]byte

// MigrationTokenSize is the wire size of a migration token.
const MigrationTokenSize = 8

// tokenTable maps migration tokens to connections for server-side lookup.
type tokenTable struct {
	mu     sync.RWMutex
	tokens map[MigrationToken]*Connection
}

func newTokenTable() *tokenTable {
	return &tokenTable{
		tokens: make(map[MigrationToken]*Connection),
	}
}

func (tt *tokenTable) get(token MigrationToken) *Connection {
	tt.mu.RLock()
	c := tt.tokens[token]
	tt.mu.RUnlock()
	return c
}

func (tt *tokenTable) put(token MigrationToken, conn *Connection) {
	tt.mu.Lock()
	tt.tokens[token] = conn
	tt.mu.Unlock()
}

func (tt *tokenTable) remove(token MigrationToken) {
	tt.mu.Lock()
	delete(tt.tokens, token)
	tt.mu.Unlock()
}

func (tt *tokenTable) count() int {
	tt.mu.RLock()
	defer tt.mu.RUnlock()
	return len(tt.tokens)
}
