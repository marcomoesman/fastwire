package fastwire

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"net/netip"
	"time"

	fwcrypto "github.com/marcomoesman/fastwire/crypto"
)

// compressionAck is the server's response to the client's compression preference.
type compressionAck byte

const (
	compressionOK           compressionAck = 0
	compressionDictMismatch compressionAck = 1
)

// RejectReason indicates why a connection was rejected.
type RejectReason byte

const (
	RejectServerFull RejectReason = 0
	RejectBanned     RejectReason = 1
)

// --- Parsed handshake packet types ---

type connectPacket struct {
	ProtocolVersion byte
	AppVersion      uint16
	PublicKey       []byte // 32 bytes (X25519)
	CipherPref      CipherSuite
	Compression     CompressionAlgorithm
	DictHash        []byte // nil if not present, 32 bytes if present
	Features        byte   // feature flags (FeatureSendBatching, FeatureConnectionMigration)
}

type challengePacket struct {
	ProtocolVersion byte
	AppVersion      uint16
	PublicKey       []byte // 32 bytes (X25519)
	ChallengeToken  [32]byte
	SelectedCipher  CipherSuite
	CompressionAck  compressionAck
	Features        byte           // negotiated feature flags
	MigrationToken  MigrationToken // present when FeatureConnectionMigration is negotiated
}

// pendingHandshake tracks a handshake in progress on the server side.
// Lightweight: only stores derived keys and challenge token until RESPONSE arrives.
type pendingHandshake struct {
	challengeToken    [32]byte
	c2sKey            []byte
	s2cKey            []byte
	suite             CipherSuite
	compression       compressionAck
	compressionConfig CompressionConfig
	layout            ChannelLayout
	congestionMode    CongestionMode
	initialCwnd       int
	features          byte
	migrationToken    MigrationToken
	createdAt         time.Time
}

func (ph *pendingHandshake) isExpired(timeout time.Duration) bool {
	return time.Since(ph.createdAt) >= timeout
}

// --- Control payload size constants ---
// These are sizes of the control payload INCLUDING the ControlType byte.

const (
	connectMinSize          = 1 + 1 + 2 + 32 + 1 + 1 + 1 + 1 // 40 (added features byte)
	connectWithDictSize     = connectMinSize + 32              // 72
	challengeMinSize        = 1 + 1 + 2 + 32 + 32 + 1 + 1 + 1 // 71 (added features byte)
	challengeWithMigration  = challengeMinSize + 8             // 79 (added migration token)
	responseSize            = 1 + 32                            // 33
	versionMismatchSize     = 1 + 1 + 2                         // 4
	rejectSize              = 1 + 1                              // 2
	heartbeatSize           = 1                                  // 1
	disconnectPaySize       = 1                                  // 1
)

// --- Marshal/Unmarshal for control payloads ---
// Each payload starts with the ControlType byte.

func marshalConnect(buf []byte, cp *connectPacket) (int, error) {
	needed := connectMinSize
	if cp.DictHash != nil {
		needed = connectWithDictSize
	}
	if len(buf) < needed {
		return 0, ErrBufferTooSmall
	}

	n := 0
	buf[n] = byte(ControlConnect)
	n++
	buf[n] = cp.ProtocolVersion
	n++
	binary.BigEndian.PutUint16(buf[n:], cp.AppVersion)
	n += 2
	copy(buf[n:], cp.PublicKey[:32])
	n += 32
	buf[n] = byte(cp.CipherPref)
	n++
	buf[n] = byte(cp.Compression)
	n++
	if cp.DictHash != nil {
		buf[n] = 1
		n++
		copy(buf[n:], cp.DictHash[:32])
		n += 32
	} else {
		buf[n] = 0
		n++
	}
	buf[n] = cp.Features
	n++
	return n, nil
}

func unmarshalConnect(data []byte) (connectPacket, error) {
	if len(data) < connectMinSize {
		return connectPacket{}, ErrInvalidHandshake
	}
	if ControlType(data[0]) != ControlConnect {
		return connectPacket{}, ErrInvalidHandshake
	}

	var cp connectPacket
	n := 1
	cp.ProtocolVersion = data[n]
	n++
	cp.AppVersion = binary.BigEndian.Uint16(data[n:])
	n += 2
	cp.PublicKey = make([]byte, 32)
	copy(cp.PublicKey, data[n:n+32])
	n += 32
	cp.CipherPref = CipherSuite(data[n])
	n++
	cp.Compression = CompressionAlgorithm(data[n])
	n++
	dictPresent := data[n]
	n++
	if dictPresent == 1 {
		if len(data) < connectWithDictSize {
			return connectPacket{}, ErrInvalidHandshake
		}
		cp.DictHash = make([]byte, 32)
		copy(cp.DictHash, data[n:n+32])
		n += 32
	}
	cp.Features = data[n]
	return cp, nil
}

func marshalChallenge(buf []byte, cp *challengePacket) (int, error) {
	needed := challengeMinSize
	if cp.Features&byte(FeatureConnectionMigration) != 0 {
		needed = challengeWithMigration
	}
	if len(buf) < needed {
		return 0, ErrBufferTooSmall
	}

	n := 0
	buf[n] = byte(ControlChallenge)
	n++
	buf[n] = cp.ProtocolVersion
	n++
	binary.BigEndian.PutUint16(buf[n:], cp.AppVersion)
	n += 2
	copy(buf[n:], cp.PublicKey[:32])
	n += 32
	copy(buf[n:], cp.ChallengeToken[:])
	n += 32
	buf[n] = byte(cp.SelectedCipher)
	n++
	buf[n] = byte(cp.CompressionAck)
	n++
	buf[n] = cp.Features
	n++
	if cp.Features&byte(FeatureConnectionMigration) != 0 {
		copy(buf[n:], cp.MigrationToken[:])
		n += MigrationTokenSize
	}
	return n, nil
}

func unmarshalChallenge(data []byte) (challengePacket, error) {
	if len(data) < challengeMinSize {
		return challengePacket{}, ErrInvalidHandshake
	}
	if ControlType(data[0]) != ControlChallenge {
		return challengePacket{}, ErrInvalidHandshake
	}

	var cp challengePacket
	n := 1
	cp.ProtocolVersion = data[n]
	n++
	cp.AppVersion = binary.BigEndian.Uint16(data[n:])
	n += 2
	cp.PublicKey = make([]byte, 32)
	copy(cp.PublicKey, data[n:n+32])
	n += 32
	copy(cp.ChallengeToken[:], data[n:n+32])
	n += 32
	cp.SelectedCipher = CipherSuite(data[n])
	n++
	cp.CompressionAck = compressionAck(data[n])
	n++
	cp.Features = data[n]
	n++
	if cp.Features&byte(FeatureConnectionMigration) != 0 {
		if len(data) < challengeWithMigration {
			return challengePacket{}, ErrInvalidHandshake
		}
		copy(cp.MigrationToken[:], data[n:n+MigrationTokenSize])
	}
	return cp, nil
}

func marshalResponse(buf []byte, token [32]byte) (int, error) {
	if len(buf) < responseSize {
		return 0, ErrBufferTooSmall
	}
	buf[0] = byte(ControlResponse)
	copy(buf[1:], token[:])
	return responseSize, nil
}

func unmarshalResponse(data []byte) ([32]byte, error) {
	var token [32]byte
	if len(data) < responseSize {
		return token, ErrInvalidHandshake
	}
	if ControlType(data[0]) != ControlResponse {
		return token, ErrInvalidHandshake
	}
	copy(token[:], data[1:33])
	return token, nil
}

func marshalVersionMismatch(buf []byte, protoVersion byte, appVersion uint16) (int, error) {
	if len(buf) < versionMismatchSize {
		return 0, ErrBufferTooSmall
	}
	buf[0] = byte(ControlVersionMismatch)
	buf[1] = protoVersion
	binary.BigEndian.PutUint16(buf[2:], appVersion)
	return versionMismatchSize, nil
}

func unmarshalVersionMismatch(data []byte) (byte, uint16, error) {
	if len(data) < versionMismatchSize {
		return 0, 0, ErrInvalidHandshake
	}
	if ControlType(data[0]) != ControlVersionMismatch {
		return 0, 0, ErrInvalidHandshake
	}
	return data[1], binary.BigEndian.Uint16(data[2:]), nil
}

func marshalReject(buf []byte, reason RejectReason) (int, error) {
	if len(buf) < rejectSize {
		return 0, ErrBufferTooSmall
	}
	buf[0] = byte(ControlReject)
	buf[1] = byte(reason)
	return rejectSize, nil
}

func unmarshalReject(data []byte) (RejectReason, error) {
	if len(data) < rejectSize {
		return 0, ErrInvalidHandshake
	}
	if ControlType(data[0]) != ControlReject {
		return 0, ErrInvalidHandshake
	}
	return RejectReason(data[1]), nil
}

func marshalHeartbeat(buf []byte) (int, error) {
	if len(buf) < heartbeatSize {
		return 0, ErrBufferTooSmall
	}
	buf[0] = byte(ControlHeartbeat)
	return heartbeatSize, nil
}

func marshalDisconnect(buf []byte) (int, error) {
	if len(buf) < disconnectPaySize {
		return 0, ErrBufferTooSmall
	}
	buf[0] = byte(ControlDisconnect)
	return disconnectPaySize, nil
}

// --- Packet building helpers ---

// buildControlPacket writes a PacketHeader (with FlagControl set) followed by controlPayload.
func buildControlPacket(buf []byte, hdr *PacketHeader, controlPayload []byte) (int, error) {
	hdr.Flags |= FlagControl
	n, err := MarshalHeader(buf, hdr)
	if err != nil {
		return 0, err
	}
	if len(buf[n:]) < len(controlPayload) {
		return 0, ErrBufferTooSmall
	}
	copy(buf[n:], controlPayload)
	return n + len(controlPayload), nil
}

// parseControlPacket parses a complete control packet.
// Returns the header, control type, and the full control payload (starting at the ControlType byte).
func parseControlPacket(data []byte) (PacketHeader, ControlType, []byte, error) {
	hdr, n, err := UnmarshalHeader(data)
	if err != nil {
		return PacketHeader{}, 0, nil, err
	}
	if hdr.Flags&FlagControl == 0 {
		return PacketHeader{}, 0, nil, ErrInvalidHandshake
	}
	if len(data[n:]) < 1 {
		return PacketHeader{}, 0, nil, ErrInvalidHandshake
	}
	ct := ControlType(data[n])
	return hdr, ct, data[n:], nil
}

// --- Convenience packet builders ---

func buildConnectPacket(buf []byte, cp *connectPacket) (int, error) {
	hdr := PacketHeader{Flags: FlagControl}
	n, err := MarshalHeader(buf, &hdr)
	if err != nil {
		return 0, err
	}
	w, err := marshalConnect(buf[n:], cp)
	if err != nil {
		return 0, err
	}
	return n + w, nil
}

func buildChallengePacket(buf []byte, cp *challengePacket) (int, error) {
	hdr := PacketHeader{Flags: FlagControl}
	n, err := MarshalHeader(buf, &hdr)
	if err != nil {
		return 0, err
	}
	w, err := marshalChallenge(buf[n:], cp)
	if err != nil {
		return 0, err
	}
	return n + w, nil
}

func buildResponsePacket(buf []byte, token [32]byte) (int, error) {
	hdr := PacketHeader{Flags: FlagControl}
	n, err := MarshalHeader(buf, &hdr)
	if err != nil {
		return 0, err
	}
	w, err := marshalResponse(buf[n:], token)
	if err != nil {
		return 0, err
	}
	return n + w, nil
}

func buildVersionMismatchPacket(buf []byte, protoVersion byte, appVersion uint16) (int, error) {
	hdr := PacketHeader{Flags: FlagControl}
	n, err := MarshalHeader(buf, &hdr)
	if err != nil {
		return 0, err
	}
	w, err := marshalVersionMismatch(buf[n:], protoVersion, appVersion)
	if err != nil {
		return 0, err
	}
	return n + w, nil
}

func buildRejectPacket(buf []byte, reason RejectReason) (int, error) {
	hdr := PacketHeader{Flags: FlagControl}
	n, err := MarshalHeader(buf, &hdr)
	if err != nil {
		return 0, err
	}
	w, err := marshalReject(buf[n:], reason)
	if err != nil {
		return 0, err
	}
	return n + w, nil
}

// --- Server-side handshake processing ---

// serverProcessConnect handles an incoming CONNECT packet on the server side.
// It validates the protocol version, generates a server key pair, creates a challenge
// token, derives keys, and builds the CHALLENGE response packet.
// Returns the pending handshake state and the CHALLENGE packet bytes.
func serverProcessConnect(data []byte, serverCompression CompressionConfig, layout ChannelLayout, congestionMode CongestionMode, initialCwnd int, serverFeatures byte) (*pendingHandshake, []byte, error) {
	// Parse the control packet.
	_, ct, ctrlPayload, err := parseControlPacket(data)
	if err != nil {
		return nil, nil, err
	}
	if ct != ControlConnect {
		return nil, nil, ErrInvalidHandshake
	}

	// Unmarshal CONNECT.
	connect, err := unmarshalConnect(ctrlPayload)
	if err != nil {
		return nil, nil, err
	}

	// Check protocol version.
	if connect.ProtocolVersion != ProtocolVersion {
		buf := make([]byte, 64)
		n, err := buildVersionMismatchPacket(buf, ProtocolVersion, ApplicationVersion)
		if err != nil {
			return nil, nil, err
		}
		return nil, buf[:n], ErrVersionMismatch
	}

	// Select cipher: accept client preference.
	selectedCipher := connect.CipherPref

	// Determine compression ack and negotiated config.
	compAck := compressionOK
	negotiatedCompression := CompressionConfig{}
	if connect.Compression != CompressionNone && serverCompression.Algorithm != connect.Compression {
		compAck = compressionDictMismatch
	} else if connect.Compression != CompressionNone && serverCompression.Algorithm == connect.Compression {
		// Algorithm matches — check dictionary hash for zstd.
		if connect.Compression == CompressionZstd && connect.DictHash != nil {
			serverHash := DictionaryHash(serverCompression.Dictionary)
			if !bytes.Equal(connect.DictHash, serverHash[:]) {
				compAck = compressionDictMismatch
			}
		}
		if compAck == compressionOK {
			negotiatedCompression = serverCompression
		}
	}

	// Generate server ephemeral key pair.
	serverKP, err := fwcrypto.GenerateKeyPair()
	if err != nil {
		return nil, nil, err
	}

	// Generate challenge token.
	var challengeToken [32]byte
	if _, err := rand.Read(challengeToken[:]); err != nil {
		return nil, nil, err
	}

	// Reconstruct client public key.
	clientPub, err := ecdh.X25519().NewPublicKey(connect.PublicKey)
	if err != nil {
		return nil, nil, ErrInvalidHandshake
	}

	// Derive keys.
	c2sKey, s2cKey, err := fwcrypto.DeriveKeys(serverKP.Private, clientPub, challengeToken[:], selectedCipher)
	if err != nil {
		return nil, nil, err
	}

	// Negotiate features.
	negotiatedFeatures := connect.Features & serverFeatures

	// Generate migration token if negotiated.
	var migrationToken MigrationToken
	if negotiatedFeatures&byte(FeatureConnectionMigration) != 0 {
		if _, err := rand.Read(migrationToken[:]); err != nil {
			return nil, nil, err
		}
	}

	// Build CHALLENGE packet.
	buf := make([]byte, 128)
	n, err := buildChallengePacket(buf, &challengePacket{
		ProtocolVersion: ProtocolVersion,
		AppVersion:      ApplicationVersion,
		PublicKey:       serverKP.Public.Bytes(),
		ChallengeToken:  challengeToken,
		SelectedCipher:  selectedCipher,
		CompressionAck:  compAck,
		Features:        negotiatedFeatures,
		MigrationToken:  migrationToken,
	})
	if err != nil {
		return nil, nil, err
	}

	pending := &pendingHandshake{
		challengeToken:    challengeToken,
		c2sKey:            c2sKey,
		s2cKey:            s2cKey,
		suite:             selectedCipher,
		compression:       compAck,
		compressionConfig: negotiatedCompression,
		layout:            layout,
		congestionMode:    congestionMode,
		initialCwnd:       initialCwnd,
		features:          negotiatedFeatures,
		migrationToken:    migrationToken,
		createdAt:         time.Now(),
	}

	return pending, buf[:n], nil
}

// clientProcessChallenge handles an incoming CHALLENGE packet on the client side.
// It validates the protocol version, derives keys, and builds the encrypted RESPONSE.
// Returns the send and recv cipher states, the selected cipher suite, and the encrypted RESPONSE bytes.
func clientProcessChallenge(data []byte, clientKP fwcrypto.KeyPair) (*fwcrypto.CipherState, *fwcrypto.CipherState, CipherSuite, []byte, error) {
	// Parse the control packet.
	_, ct, ctrlPayload, err := parseControlPacket(data)
	if err != nil {
		return nil, nil, 0, nil, err
	}
	if ct != ControlChallenge {
		return nil, nil, 0, nil, ErrInvalidHandshake
	}

	// Unmarshal CHALLENGE.
	challenge, err := unmarshalChallenge(ctrlPayload)
	if err != nil {
		return nil, nil, 0, nil, err
	}

	// Check protocol version.
	if challenge.ProtocolVersion != ProtocolVersion {
		return nil, nil, 0, nil, ErrVersionMismatch
	}

	// Reconstruct server public key.
	serverPub, err := ecdh.X25519().NewPublicKey(challenge.PublicKey)
	if err != nil {
		return nil, nil, 0, nil, ErrInvalidHandshake
	}

	// Derive keys.
	c2sKey, s2cKey, err := fwcrypto.DeriveKeys(clientKP.Private, serverPub, challenge.ChallengeToken[:], challenge.SelectedCipher)
	if err != nil {
		return nil, nil, 0, nil, err
	}

	// Client: send = c2s, recv = s2c.
	sendCipher, err := fwcrypto.NewCipherState(c2sKey, challenge.SelectedCipher)
	if err != nil {
		return nil, nil, 0, nil, err
	}
	recvCipher, err := fwcrypto.NewCipherState(s2cKey, challenge.SelectedCipher)
	if err != nil {
		return nil, nil, 0, nil, err
	}

	// Build RESPONSE packet (plaintext -- caller encrypts).
	respBuf := make([]byte, 64)
	rn, err := buildResponsePacket(respBuf, challenge.ChallengeToken)
	if err != nil {
		return nil, nil, 0, nil, err
	}

	// Encrypt the RESPONSE.
	encrypted, err := fwcrypto.Encrypt(sendCipher, respBuf[:rn], nil)
	if err != nil {
		return nil, nil, 0, nil, err
	}

	return sendCipher, recvCipher, challenge.SelectedCipher, encrypted, nil
}

// serverProcessResponse handles an encrypted RESPONSE packet on the server side.
// It decrypts the packet, verifies the challenge token, and creates the Connection.
func serverProcessResponse(encryptedData []byte, pending *pendingHandshake, addr netip.AddrPort) (*Connection, error) {
	// Server: recv = c2s (client-to-server), send = s2c (server-to-client).
	recvCipher, err := fwcrypto.NewCipherState(pending.c2sKey, pending.suite)
	if err != nil {
		return nil, err
	}

	// Decrypt.
	decrypted, err := fwcrypto.Decrypt(recvCipher, encryptedData, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	// Parse control packet.
	_, ct, ctrlPayload, err := parseControlPacket(decrypted)
	if err != nil {
		return nil, ErrInvalidHandshake
	}
	if ct != ControlResponse {
		return nil, ErrInvalidHandshake
	}

	// Verify challenge token.
	token, err := unmarshalResponse(ctrlPayload)
	if err != nil {
		return nil, err
	}
	if token != pending.challengeToken {
		return nil, ErrInvalidHandshake
	}

	// Create send cipher state.
	sendCipher, err := fwcrypto.NewCipherState(pending.s2cKey, pending.suite)
	if err != nil {
		return nil, err
	}

	return newConnection(addr, sendCipher, recvCipher, pending.suite, pending.layout, pending.compressionConfig, pending.congestionMode, pending.initialCwnd, pending.features, pending.migrationToken), nil
}
