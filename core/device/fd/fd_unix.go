//go:build !windows

package fd

import (
	"fmt"
	"strconv"

	"go-tun2socks-build/core/device"
	"golang.org/x/sys/unix"
	"inet.af/netstack/tcpip/stack"
)

type FD struct {
	stack.LinkEndpoint

	fd  int
	mtu uint32
}

func Open(name string, mtu uint32) (device.Device, error) {
	fd, err := strconv.Atoi(name)
	if err != nil {
		return nil, fmt.Errorf("cannot open fd: %s", name)
	}
	if mtu == 0 {
		mtu = defaultMTU
	}
	return open(fd, mtu)
}

func (f *FD) Type() string {
	return Driver
}

func (f *FD) Name() string {
	return strconv.Itoa(f.fd)
}

func (f *FD) Close() error {
	return unix.Close(f.fd)
}

var _ device.Device = (*FD)(nil)