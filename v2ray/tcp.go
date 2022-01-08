package v2ray

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	vcore "github.com/v2fly/v2ray-core/v4"
	pool "github.com/v2fly/v2ray-core/v4/common/bytespool"
	vnet "github.com/v2fly/v2ray-core/v4/common/net"
	vsession "github.com/v2fly/v2ray-core/v4/common/session"

	M "go-tun2socks-build/constant"
	"go-tun2socks-build/core"
)

const (
	tcpWaitTimeout = 5 * time.Second
)

func handleTCP(localConn core.TCPConn, v *vcore.Instance) {
	defer localConn.Close()

	id := localConn.ID()
	metadata := &M.Metadata{
		Net:     M.TCP,
		SrcIP:   net.IP(id.RemoteAddress),
		SrcPort: id.RemotePort,
		DstIP:   net.IP(id.LocalAddress),
		DstPort: id.LocalPort,
	}

	dest := vnet.DestinationFromAddr(&net.TCPAddr{IP: metadata.DstIP, Port: int(metadata.DstPort)})
	ctx := vsession.ContextWithID(context.Background(), vsession.NewID())

	targetConn, err := vcore.Dial(ctx, v, dest)
	if err != nil {
		// log.Printf("[TCP] dial %s error: %v", metadata.DestinationAddress(), err)
		return
	}

	if dialerAddr, ok := targetConn.LocalAddr().(*net.TCPAddr); ok {
		metadata.MidIP = dialerAddr.IP
		metadata.MidPort = uint16(dialerAddr.Port)
	} else { /* fallback */
		metadata.MidIP, metadata.MidPort = parseAddr(targetConn.LocalAddr().String())
	}

	defer targetConn.Close()

	relay(localConn, targetConn) /* relay connections */
}

// relay copies between left and right bidirectionally.
func relay(left, right net.Conn) {
	wg := sync.WaitGroup{}
	wg.Add(2)

	go func() {
		defer wg.Done()
		_ = copyBuffer(right, left) /* ignore error */
		right.SetReadDeadline(time.Now().Add(tcpWaitTimeout))
	}()

	go func() {
		defer wg.Done()
		_ = copyBuffer(left, right) /* ignore error */
		left.SetReadDeadline(time.Now().Add(tcpWaitTimeout))
	}()

	wg.Wait()
}

func copyBuffer(dst io.Writer, src io.Reader) error {
	buf := pool.Alloc(RelayBufferSize)
	defer pool.Free(buf)

	_, err := io.CopyBuffer(dst, src, buf)
	return err
}
