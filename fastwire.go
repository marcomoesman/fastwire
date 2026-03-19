// Package fastwire is an enhanced UDP networking library for Go,
// designed for fast-paced, low-latency applications such as gaming.
//
// FastWire provides reliable and unreliable delivery modes, automatic
// fragmentation and reassembly, compression, encryption, and congestion
// control — all built on top of standard UDP datagrams.
package fastwire

// ProtocolVersion is the current FastWire wire protocol version.
// Mismatched versions are rejected during the handshake.
const ProtocolVersion byte = 1

// ApplicationVersion can be set by the application using FastWire to
// identify its own protocol version (e.g. a game's network protocol version).
// This is transmitted during the handshake alongside ProtocolVersion.
var ApplicationVersion uint16 = 0
