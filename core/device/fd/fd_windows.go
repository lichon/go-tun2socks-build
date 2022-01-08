package fd

import (
	"errors"

	"go-tun2socks-build/core/device"
)

func Open(name string, mtu uint32) (device.Device, error) {
	return nil, errors.New("not supported")
}
