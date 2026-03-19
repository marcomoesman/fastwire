package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestGenerateKeyPair(t *testing.T) {
	kp1, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if kp1.Private == nil || kp1.Public == nil {
		t.Fatal("key pair fields must not be nil")
	}

	kp2, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(kp1.Private.Bytes(), kp2.Private.Bytes()) {
		t.Fatal("two generated key pairs should differ")
	}
}

func TestDeriveKeysBothSidesSame(t *testing.T) {
	client, _ := GenerateKeyPair()
	server, _ := GenerateKeyPair()

	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		t.Fatal(err)
	}

	clientC2S, clientS2C, err := DeriveKeys(client.Private, server.Public, token, CipherAES128GCM)
	if err != nil {
		t.Fatal(err)
	}
	serverC2S, serverS2C, err := DeriveKeys(server.Private, client.Public, token, CipherAES128GCM)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(clientC2S, serverC2S) {
		t.Fatal("c2s keys differ between client and server")
	}
	if !bytes.Equal(clientS2C, serverS2C) {
		t.Fatal("s2c keys differ between client and server")
	}
}

func TestDeriveKeysBothSuites(t *testing.T) {
	client, _ := GenerateKeyPair()
	server, _ := GenerateKeyPair()
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		t.Fatal(err)
	}

	// AES-128-GCM: 16-byte keys.
	c2s, s2c, err := DeriveKeys(client.Private, server.Public, token, CipherAES128GCM)
	if err != nil {
		t.Fatal(err)
	}
	if len(c2s) != 16 || len(s2c) != 16 {
		t.Fatalf("AES key sizes: c2s=%d, s2c=%d, want 16", len(c2s), len(s2c))
	}

	// ChaCha20-Poly1305: 32-byte keys.
	c2s, s2c, err = DeriveKeys(client.Private, server.Public, token, CipherChaCha20Poly1305)
	if err != nil {
		t.Fatal(err)
	}
	if len(c2s) != 32 || len(s2c) != 32 {
		t.Fatalf("ChaCha key sizes: c2s=%d, s2c=%d, want 32", len(c2s), len(s2c))
	}
}

func TestDeriveKeysDeterministic(t *testing.T) {
	client, _ := GenerateKeyPair()
	server, _ := GenerateKeyPair()
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		t.Fatal(err)
	}

	c2s1, s2c1, _ := DeriveKeys(client.Private, server.Public, token, CipherAES128GCM)
	c2s2, s2c2, _ := DeriveKeys(client.Private, server.Public, token, CipherAES128GCM)

	if !bytes.Equal(c2s1, c2s2) {
		t.Fatal("c2s keys should be deterministic")
	}
	if !bytes.Equal(s2c1, s2c2) {
		t.Fatal("s2c keys should be deterministic")
	}
}

func TestDeriveKeysDifferentTokens(t *testing.T) {
	client, _ := GenerateKeyPair()
	server, _ := GenerateKeyPair()

	token1 := make([]byte, 32)
	token2 := make([]byte, 32)
	if _, err := rand.Read(token1); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(token2); err != nil {
		t.Fatal(err)
	}

	c2s1, s2c1, _ := DeriveKeys(client.Private, server.Public, token1, CipherAES128GCM)
	c2s2, s2c2, _ := DeriveKeys(client.Private, server.Public, token2, CipherAES128GCM)

	if bytes.Equal(c2s1, c2s2) {
		t.Fatal("different tokens should produce different c2s keys")
	}
	if bytes.Equal(s2c1, s2c2) {
		t.Fatal("different tokens should produce different s2c keys")
	}
}

func TestDeriveAndEncryptRoundTrip(t *testing.T) {
	client, _ := GenerateKeyPair()
	server, _ := GenerateKeyPair()
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		t.Fatal(err)
	}

	suites := []struct {
		name  string
		suite CipherSuite
	}{
		{"AES128GCM", CipherAES128GCM},
		{"ChaCha20Poly1305", CipherChaCha20Poly1305},
	}

	for _, s := range suites {
		t.Run(s.name, func(t *testing.T) {
			// Client derives keys.
			c2sKey, s2cKey, err := DeriveKeys(client.Private, server.Public, token, s.suite)
			if err != nil {
				t.Fatal(err)
			}

			// Server derives keys.
			srvC2S, srvS2C, err := DeriveKeys(server.Private, client.Public, token, s.suite)
			if err != nil {
				t.Fatal(err)
			}

			// Client: send=c2s, recv=s2c
			clientSend, err := NewCipherState(c2sKey, s.suite)
			if err != nil {
				t.Fatal(err)
			}
			clientRecv, err := NewCipherState(s2cKey, s.suite)
			if err != nil {
				t.Fatal(err)
			}

			// Server: send=s2c, recv=c2s
			serverSend, err := NewCipherState(srvS2C, s.suite)
			if err != nil {
				t.Fatal(err)
			}
			serverRecv, err := NewCipherState(srvC2S, s.suite)
			if err != nil {
				t.Fatal(err)
			}

			// Client -> Server
			msg := []byte("client to server message")
			encrypted, err := Encrypt(clientSend, msg, nil)
			if err != nil {
				t.Fatal(err)
			}
			decrypted, err := Decrypt(serverRecv, encrypted, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(decrypted, msg) {
				t.Fatal("client->server round-trip failed")
			}

			// Server -> Client
			msg2 := []byte("server to client message")
			encrypted, err = Encrypt(serverSend, msg2, nil)
			if err != nil {
				t.Fatal(err)
			}
			decrypted, err = Decrypt(clientRecv, encrypted, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(decrypted, msg2) {
				t.Fatal("server->client round-trip failed")
			}
		})
	}
}
