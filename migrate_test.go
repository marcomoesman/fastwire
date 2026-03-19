package fastwire

import (
	"net/netip"
	"testing"

	fwcrypto "github.com/marcomoesman/fastwire/crypto"
)

func TestTokenTablePutGet(t *testing.T) {
	tt := newTokenTable()
	token := MigrationToken{1, 2, 3, 4, 5, 6, 7, 8}

	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(addr, send, recv, CipherNone, DefaultChannelLayout(), CompressionConfig{}, CongestionConservative, 0, 0, MigrationToken{})

	tt.put(token, conn)

	got := tt.get(token)
	if got != conn {
		t.Fatal("get returned wrong connection")
	}

	if tt.count() != 1 {
		t.Fatalf("count = %d, want 1", tt.count())
	}
}

func TestTokenTableRemove(t *testing.T) {
	tt := newTokenTable()
	token := MigrationToken{1, 2, 3, 4, 5, 6, 7, 8}

	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(addr, send, recv, CipherNone, DefaultChannelLayout(), CompressionConfig{}, CongestionConservative, 0, 0, MigrationToken{})

	tt.put(token, conn)
	tt.remove(token)

	if got := tt.get(token); got != nil {
		t.Fatal("expected nil after remove")
	}
	if tt.count() != 0 {
		t.Fatalf("count = %d, want 0", tt.count())
	}
}

func TestTokenTableMiss(t *testing.T) {
	tt := newTokenTable()
	token := MigrationToken{9, 9, 9, 9, 9, 9, 9, 9}
	if got := tt.get(token); got != nil {
		t.Fatal("expected nil for unknown token")
	}
}
