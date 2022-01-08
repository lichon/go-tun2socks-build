//go:build !linux && !windows

package fd

import (
	"fmt"
	"os"

	"go-tun2socks-build/core/device"
	"go-tun2socks-build/core/device/rwbased"
)

func open(fd int, mtu uint32) (device.Device, error) {
	f := &FD{fd: fd, mtu: mtu}

	ep, err := rwbased.New(os.NewFile(uintptr(fd), f.Name()), mtu)
	if err != nil {
		return nil, fmt.Errorf("create endpoint: %w", err)
	}
	f.LinkEndpoint = ep

	return f, nil
}
