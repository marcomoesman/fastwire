package fastwire

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net/netip"
	"testing"
	"time"

	fwcrypto "github.com/marcomoesman/fastwire/crypto"
)

// --- Marshal/Unmarshal round-trip tests ---

func TestMarshalUnmarshalConnect(t *testing.T) {
	kp, _ := fwcrypto.GenerateKeyPair()
	cp := connectPacket{
		ProtocolVersion: ProtocolVersion,
		AppVersion:      42,
		PublicKey:       kp.Public.Bytes(),
		CipherPref:      CipherAES128GCM,
		Compression:     CompressionNone,
	}

	buf := make([]byte, 128)
	n, err := marshalConnect(buf, &cp)
	if err != nil {
		t.Fatal(err)
	}
	if n != connectMinSize {
		t.Fatalf("size = %d, want %d", n, connectMinSize)
	}

	parsed, err := unmarshalConnect(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ProtocolVersion != cp.ProtocolVersion {
		t.Fatalf("proto = %d, want %d", parsed.ProtocolVersion, cp.ProtocolVersion)
	}
	if parsed.AppVersion != cp.AppVersion {
		t.Fatalf("app = %d, want %d", parsed.AppVersion, cp.AppVersion)
	}
	if !bytes.Equal(parsed.PublicKey, cp.PublicKey) {
		t.Fatal("public key mismatch")
	}
	if parsed.CipherPref != cp.CipherPref {
		t.Fatalf("cipher = %d, want %d", parsed.CipherPref, cp.CipherPref)
	}
	if parsed.Compression != cp.Compression {
		t.Fatalf("compression = %d, want %d", parsed.Compression, cp.Compression)
	}
	if parsed.DictHash != nil {
		t.Fatal("dict hash should be nil")
	}
}

func TestMarshalUnmarshalConnectWithDictHash(t *testing.T) {
	kp, _ := fwcrypto.GenerateKeyPair()
	dictHash := sha256.Sum256([]byte("test dictionary"))
	cp := connectPacket{
		ProtocolVersion: ProtocolVersion,
		AppVersion:      1,
		PublicKey:       kp.Public.Bytes(),
		CipherPref:      CipherChaCha20Poly1305,
		Compression:     CompressionZstd,
		DictHash:        dictHash[:],
	}

	buf := make([]byte, 128)
	n, err := marshalConnect(buf, &cp)
	if err != nil {
		t.Fatal(err)
	}
	if n != connectWithDictSize {
		t.Fatalf("size = %d, want %d", n, connectWithDictSize)
	}

	parsed, err := unmarshalConnect(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(parsed.DictHash, dictHash[:]) {
		t.Fatal("dict hash mismatch")
	}
}

func TestMarshalUnmarshalChallenge(t *testing.T) {
	kp, _ := fwcrypto.GenerateKeyPair()
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		t.Fatal(err)
	}

	cp := challengePacket{
		ProtocolVersion: ProtocolVersion,
		AppVersion:      42,
		PublicKey:       kp.Public.Bytes(),
		ChallengeToken:  token,
		SelectedCipher:  CipherAES128GCM,
		CompressionAck:  compressionOK,
	}

	buf := make([]byte, 128)
	n, err := marshalChallenge(buf, &cp)
	if err != nil {
		t.Fatal(err)
	}
	if n != challengeMinSize {
		t.Fatalf("size = %d, want %d", n, challengeMinSize)
	}

	parsed, err := unmarshalChallenge(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ProtocolVersion != cp.ProtocolVersion {
		t.Fatal("proto mismatch")
	}
	if parsed.ChallengeToken != cp.ChallengeToken {
		t.Fatal("token mismatch")
	}
	if parsed.SelectedCipher != cp.SelectedCipher {
		t.Fatal("cipher mismatch")
	}
	if parsed.CompressionAck != cp.CompressionAck {
		t.Fatal("compression ack mismatch")
	}
}

func TestMarshalUnmarshalResponse(t *testing.T) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 64)
	n, err := marshalResponse(buf, token)
	if err != nil {
		t.Fatal(err)
	}
	if n != responseSize {
		t.Fatalf("size = %d, want %d", n, responseSize)
	}

	parsed, err := unmarshalResponse(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if parsed != token {
		t.Fatal("token mismatch")
	}
}

func TestMarshalUnmarshalVersionMismatch(t *testing.T) {
	buf := make([]byte, 16)
	n, err := marshalVersionMismatch(buf, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	if n != versionMismatchSize {
		t.Fatalf("size = %d, want %d", n, versionMismatchSize)
	}

	proto, app, err := unmarshalVersionMismatch(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if proto != 2 {
		t.Fatalf("proto = %d, want 2", proto)
	}
	if app != 100 {
		t.Fatalf("app = %d, want 100", app)
	}
}

func TestMarshalUnmarshalReject(t *testing.T) {
	buf := make([]byte, 16)
	n, err := marshalReject(buf, RejectServerFull)
	if err != nil {
		t.Fatal(err)
	}
	if n != rejectSize {
		t.Fatalf("size = %d, want %d", n, rejectSize)
	}

	reason, err := unmarshalReject(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if reason != RejectServerFull {
		t.Fatalf("reason = %d, want RejectServerFull", reason)
	}
}

// --- parseControlPacket tests ---

func TestParseControlPacket(t *testing.T) {
	kp, _ := fwcrypto.GenerateKeyPair()
	cp := connectPacket{
		ProtocolVersion: ProtocolVersion,
		AppVersion:      1,
		PublicKey:       kp.Public.Bytes(),
		CipherPref:      CipherAES128GCM,
		Compression:     CompressionNone,
	}

	buf := make([]byte, 128)
	n, err := buildConnectPacket(buf, &cp)
	if err != nil {
		t.Fatal(err)
	}

	hdr, ct, payload, err := parseControlPacket(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Flags&FlagControl == 0 {
		t.Fatal("FlagControl not set")
	}
	if ct != ControlConnect {
		t.Fatalf("ct = %d, want ControlConnect", ct)
	}

	// Verify the payload can be unmarshaled.
	parsed, err := unmarshalConnect(payload)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ProtocolVersion != ProtocolVersion {
		t.Fatal("proto mismatch")
	}
}

func TestParseControlPacketNotControl(t *testing.T) {
	// Build a packet without FlagControl.
	hdr := PacketHeader{Flags: 0}
	buf := make([]byte, 64)
	n, err := MarshalHeader(buf, &hdr)
	if err != nil {
		t.Fatal(err)
	}
	buf[n] = byte(ControlHeartbeat)

	_, _, _, err = parseControlPacket(buf[:n+1])
	if !errors.Is(err, ErrInvalidHandshake) {
		t.Fatalf("expected ErrInvalidHandshake, got %v", err)
	}
}

// --- Full handshake tests ---

func testFullHandshake(t *testing.T, suite CipherSuite) {
	t.Helper()
	addr := netip.MustParseAddrPort("127.0.0.1:9000")

	// 1. Client generates key pair and builds CONNECT.
	clientKP, err := fwcrypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	connectBuf := make([]byte, 128)
	cn, err := buildConnectPacket(connectBuf, &connectPacket{
		ProtocolVersion: ProtocolVersion,
		AppVersion:      ApplicationVersion,
		PublicKey:       clientKP.Public.Bytes(),
		CipherPref:      suite,
		Compression:     CompressionNone,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Server processes CONNECT → returns pending + CHALLENGE.
	pending, challengeBytes, err := serverProcessConnect(connectBuf[:cn], CompressionConfig{}, DefaultChannelLayout(), CongestionConservative, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pending == nil {
		t.Fatal("pending is nil")
	}
	if pending.suite != suite {
		t.Fatalf("suite = %d, want %d", pending.suite, suite)
	}

	// 3. Client processes CHALLENGE → gets cipher states + encrypted RESPONSE.
	sendCipher, recvCipher, selectedSuite, encryptedResp, err := clientProcessChallenge(challengeBytes, clientKP)
	if err != nil {
		t.Fatal(err)
	}
	if selectedSuite != suite {
		t.Fatalf("selected suite = %d, want %d", selectedSuite, suite)
	}
	if sendCipher == nil || recvCipher == nil {
		t.Fatal("cipher states are nil")
	}

	// 4. Server processes encrypted RESPONSE → creates Connection.
	conn, err := serverProcessResponse(encryptedResp, pending, addr)
	if err != nil {
		t.Fatal(err)
	}
	if conn.State() != StateConnected {
		t.Fatalf("state = %v, want StateConnected", conn.State())
	}
	if conn.RemoteAddr() != addr {
		t.Fatalf("addr = %v, want %v", conn.RemoteAddr(), addr)
	}

	// 5. Verify bidirectional communication.
	msg := []byte("hello from client")
	encrypted, err := fwcrypto.Encrypt(sendCipher, msg, nil)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := fwcrypto.Decrypt(conn.recvCipher, encrypted, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, msg) {
		t.Fatal("client->server message mismatch")
	}

	msg2 := []byte("hello from server")
	encrypted, err = fwcrypto.Encrypt(conn.sendCipher, msg2, nil)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err = fwcrypto.Decrypt(recvCipher, encrypted, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, msg2) {
		t.Fatal("server->client message mismatch")
	}
}

func TestFullHandshakeAES(t *testing.T) {
	testFullHandshake(t, CipherAES128GCM)
}

func TestFullHandshakeChaCha(t *testing.T) {
	testFullHandshake(t, CipherChaCha20Poly1305)
}

func TestFullHandshakeCipherNone(t *testing.T) {
	testFullHandshake(t, CipherNone)
}

// --- Version mismatch test ---

func TestHandshakeVersionMismatch(t *testing.T) {
	clientKP, _ := fwcrypto.GenerateKeyPair()

	// Client sends CONNECT with wrong protocol version.
	connectBuf := make([]byte, 128)
	cn, err := buildConnectPacket(connectBuf, &connectPacket{
		ProtocolVersion: ProtocolVersion + 1, // wrong version
		AppVersion:      0,
		PublicKey:       clientKP.Public.Bytes(),
		CipherPref:      CipherAES128GCM,
		Compression:     CompressionNone,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, mismatchBytes, err := serverProcessConnect(connectBuf[:cn], CompressionConfig{}, DefaultChannelLayout(), CongestionConservative, 0, 0)
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("expected ErrVersionMismatch, got %v", err)
	}

	// The server should have returned a VERSION_MISMATCH packet.
	if mismatchBytes == nil {
		t.Fatal("mismatch packet is nil")
	}

	_, ct, payload, err := parseControlPacket(mismatchBytes)
	if err != nil {
		t.Fatal(err)
	}
	if ct != ControlVersionMismatch {
		t.Fatalf("ct = %d, want ControlVersionMismatch", ct)
	}

	proto, _, err := unmarshalVersionMismatch(payload)
	if err != nil {
		t.Fatal(err)
	}
	if proto != ProtocolVersion {
		t.Fatalf("server version = %d, want %d", proto, ProtocolVersion)
	}
}

// --- Cipher negotiation test ---

func TestHandshakeCipherNegotiation(t *testing.T) {
	suites := []CipherSuite{CipherNone, CipherAES128GCM, CipherChaCha20Poly1305}

	for _, pref := range suites {
		t.Run(cipherName(pref), func(t *testing.T) {
			clientKP, _ := fwcrypto.GenerateKeyPair()

			connectBuf := make([]byte, 128)
			cn, _ := buildConnectPacket(connectBuf, &connectPacket{
				ProtocolVersion: ProtocolVersion,
				AppVersion:      0,
				PublicKey:       clientKP.Public.Bytes(),
				CipherPref:      pref,
				Compression:     CompressionNone,
			})

			pending, _, err := serverProcessConnect(connectBuf[:cn], CompressionConfig{}, DefaultChannelLayout(), CongestionConservative, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if pending.suite != pref {
				t.Fatalf("negotiated suite = %d, want %d", pending.suite, pref)
			}
		})
	}
}

func cipherName(s CipherSuite) string {
	switch s {
	case CipherNone:
		return "None"
	case CipherAES128GCM:
		return "AES128GCM"
	case CipherChaCha20Poly1305:
		return "ChaCha20Poly1305"
	default:
		return "Unknown"
	}
}

// --- Compression ack tests ---

func TestHandshakeCompressionOK(t *testing.T) {
	clientKP, _ := fwcrypto.GenerateKeyPair()

	connectBuf := make([]byte, 128)
	cn, _ := buildConnectPacket(connectBuf, &connectPacket{
		ProtocolVersion: ProtocolVersion,
		AppVersion:      0,
		PublicKey:       clientKP.Public.Bytes(),
		CipherPref:      CipherAES128GCM,
		Compression:     CompressionNone,
	})

	pending, _, err := serverProcessConnect(connectBuf[:cn], CompressionConfig{Algorithm: CompressionNone}, DefaultChannelLayout(), CongestionConservative, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pending.compression != compressionOK {
		t.Fatalf("compression ack = %d, want compressionOK", pending.compression)
	}
}

func TestHandshakeCompressionDictMismatch(t *testing.T) {
	clientKP, _ := fwcrypto.GenerateKeyPair()

	connectBuf := make([]byte, 128)
	cn, _ := buildConnectPacket(connectBuf, &connectPacket{
		ProtocolVersion: ProtocolVersion,
		AppVersion:      0,
		PublicKey:       clientKP.Public.Bytes(),
		CipherPref:      CipherAES128GCM,
		Compression:     CompressionLZ4, // client wants LZ4
	})

	// Server has zstd configured — mismatch.
	pending, _, err := serverProcessConnect(connectBuf[:cn], CompressionConfig{Algorithm: CompressionZstd}, DefaultChannelLayout(), CongestionConservative, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pending.compression != compressionDictMismatch {
		t.Fatalf("compression ack = %d, want compressionDictMismatch", pending.compression)
	}
}

// --- Pending handshake expiry test ---

func TestPendingHandshakeExpiry(t *testing.T) {
	ph := &pendingHandshake{
		createdAt: time.Now().Add(-10 * time.Second),
	}
	if !ph.isExpired(5 * time.Second) {
		t.Fatal("should be expired after 10s with 5s timeout")
	}

	ph2 := &pendingHandshake{
		createdAt: time.Now(),
	}
	if ph2.isExpired(5 * time.Second) {
		t.Fatal("should not be expired immediately")
	}
}

// --- Server rejects tampered RESPONSE ---

func TestServerRejectsTamperedResponse(t *testing.T) {
	clientKP, _ := fwcrypto.GenerateKeyPair()
	addr := netip.MustParseAddrPort("127.0.0.1:9000")

	connectBuf := make([]byte, 128)
	cn, _ := buildConnectPacket(connectBuf, &connectPacket{
		ProtocolVersion: ProtocolVersion,
		AppVersion:      0,
		PublicKey:       clientKP.Public.Bytes(),
		CipherPref:      CipherAES128GCM,
		Compression:     CompressionNone,
	})

	pending, challengeBytes, err := serverProcessConnect(connectBuf[:cn], CompressionConfig{}, DefaultChannelLayout(), CongestionConservative, 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Client processes challenge normally.
	_, _, _, encryptedResp, err := clientProcessChallenge(challengeBytes, clientKP)
	if err != nil {
		t.Fatal(err)
	}

	// Tamper with the encrypted response.
	if len(encryptedResp) > 10 {
		encryptedResp[10] ^= 0xFF
	}

	_, err = serverProcessResponse(encryptedResp, pending, addr)
	if err == nil {
		t.Fatal("expected error for tampered response")
	}
}

// --- Server rejects wrong challenge token ---

func TestServerRejectsWrongToken(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	clientKP, _ := fwcrypto.GenerateKeyPair()

	connectBuf := make([]byte, 128)
	cn, _ := buildConnectPacket(connectBuf, &connectPacket{
		ProtocolVersion: ProtocolVersion,
		AppVersion:      0,
		PublicKey:       clientKP.Public.Bytes(),
		CipherPref:      CipherNone, // use CipherNone so we can forge the plaintext
		Compression:     CompressionNone,
	})

	pending, _, err := serverProcessConnect(connectBuf[:cn], CompressionConfig{}, DefaultChannelLayout(), CongestionConservative, 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Build a RESPONSE with a wrong token.
	var wrongToken [32]byte
	if _, err := rand.Read(wrongToken[:]); err != nil {
		t.Fatal(err)
	}
	respBuf := make([]byte, 64)
	rn, _ := buildResponsePacket(respBuf, wrongToken)

	// With CipherNone, encrypt is pass-through.
	fakeSend, _ := fwcrypto.NewCipherState(pending.c2sKey, CipherNone)
	encrypted, _ := fwcrypto.Encrypt(fakeSend, respBuf[:rn], nil)

	_, err = serverProcessResponse(encrypted, pending, addr)
	if !errors.Is(err, ErrInvalidHandshake) {
		t.Fatalf("expected ErrInvalidHandshake, got %v", err)
	}
}

// --- Fuzz tests ---

func FuzzUnmarshalConnect(f *testing.F) {
	kp, _ := fwcrypto.GenerateKeyPair()
	buf := make([]byte, 128)
	n, _ := marshalConnect(buf, &connectPacket{
		ProtocolVersion: 1,
		AppVersion:      0,
		PublicKey:       kp.Public.Bytes(),
		CipherPref:      CipherAES128GCM,
		Compression:     CompressionNone,
	})
	f.Add(buf[:n])
	f.Add([]byte{})
	f.Add(make([]byte, 10))

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = unmarshalConnect(data) // must not panic
	})
}

func FuzzUnmarshalChallenge(f *testing.F) {
	kp, _ := fwcrypto.GenerateKeyPair()
	var token [32]byte
	buf := make([]byte, 128)
	n, _ := marshalChallenge(buf, &challengePacket{
		ProtocolVersion: 1,
		AppVersion:      0,
		PublicKey:       kp.Public.Bytes(),
		ChallengeToken:  token,
		SelectedCipher:  CipherAES128GCM,
		CompressionAck:  compressionOK,
	})
	f.Add(buf[:n])
	f.Add([]byte{})
	f.Add(make([]byte, 10))

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = unmarshalChallenge(data) // must not panic
	})
}
