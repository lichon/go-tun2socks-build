package tun2socks

import (
	"errors"
	"fmt"

	"github.com/katalix/go-l2tp/config"
	"github.com/katalix/go-l2tp/l2tp"
)

// PacketFlow should be implemented in Java/Kotlin.
type PacketFlow interface {
	// WritePacket should writes packets to the TUN fd.
	Write(packet []byte) (int, error)
}

// VpnService should be implemented in Java/Kotlin.
type VpnService interface {
	// Protect is just a proxy to the VpnService.protect() method.
	// See also: https://developer.android.com/reference/android/net/VpnService.html#protect(int)
	Protect(fd int) bool
}

// StartL2tp
// connection handler for l2tp
func StartL2tp(
	packetFlow PacketFlow,
	vpnService VpnService,
	configBytes []byte) error {
	if packetFlow != nil {
		logger := nil
		l2tpConfig, err := config.LoadString(string(configBytes))
		// Start the L2tp session
		l2tpCtx, err := l2tp.NewContext(nil, logger)
		if err != nil {
			return errors.New(fmt.Sprintf("failed to create L2TP context: %v", err))
		}

		return nil
	}
	return errors.New("packetFlow is null")
}

// StopL2tp
func StopV2tp() {
}
