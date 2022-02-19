package udt

import (
	"time"

	"github.com/PeernetOfficial/core/udt/packet"
)

// Config controls behavior of sockets created with it
type Config struct {
	CanAcceptDgram     bool          // can this listener accept datagrams?
	CanAcceptStream    bool          // can this listener accept streams?
	ListenReplayWindow time.Duration // length of time to wait for repeated incoming connections
	MaxPacketSize      uint          // Upper limit on maximum packet size (0 = unlimited)
	MaxBandwidth       uint64        // Maximum bandwidth to take with this connection (in bytes/sec, 0 = unlimited)
	LingerTime         time.Duration // time to wait for retransmit requests after connection shutdown
	MaxFlowWinSize     uint          // maximum number of unacknowledged packets to permit (minimum 32)
	SynTime            time.Duration // SynTime

	CanAccept           func(hsPacket *packet.HandshakePacket) error // can this listener accept this connection?
	CongestionForSocket func(sock *udtSocket) CongestionControl      // create or otherwise return the CongestionControl for this socket
}

// DefaultConfig constructs a Config with default values
func DefaultConfig() *Config {
	return &Config{
		CanAcceptDgram:     true,
		CanAcceptStream:    true,
		ListenReplayWindow: 5 * time.Minute,
		LingerTime:         10 * time.Second,
		MaxFlowWinSize:     64,
		MaxBandwidth:       0,
		MaxPacketSize:      65535,
		SynTime:            10000 * time.Microsecond,
		CongestionForSocket: func(sock *udtSocket) CongestionControl {
			return &NativeCongestionControl{}
		},
	}
}
