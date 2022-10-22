package v2ray

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"time"

	vcore "github.com/v2fly/v2ray-core/v4"
	vbuf "github.com/v2fly/v2ray-core/v4/common/buf"
	vnet "github.com/v2fly/v2ray-core/v4/common/net"
	"github.com/v2fly/v2ray-core/v4/common/protocol/udp"
	vsession "github.com/v2fly/v2ray-core/v4/common/session"
	vsignal "github.com/v2fly/v2ray-core/v4/common/signal"
	"github.com/v2fly/v2ray-core/v4/common/signal/done"
	vrouting "github.com/v2fly/v2ray-core/v4/features/routing"
	vudptrans "github.com/v2fly/v2ray-core/v4/transport/internet/udp"

	"go-tun2socks-build/core/nat"

	"go-tun2socks-build/common/pool"
	M "go-tun2socks-build/constant"
	"go-tun2socks-build/core"
)

var (
	// _natTable uses source udp packet information
	// as key to store destination udp packetConn.
	_natTable = nat.NewTable()

	// _udpSessionTimeout is the default timeout for
	// each UDP session.
	_udpSessionTimeout = 90 * time.Second
)

func SetUDPTimeout(v int) {
	_udpSessionTimeout = time.Duration(v) * time.Second
}

func handleUDP(packet core.UDPPacket, v *vcore.Instance) {
	id := packet.ID()
	metadata := &M.Metadata{
		Net:     M.UDP,
		SrcIP:   net.IP(id.RemoteAddress),
		SrcPort: id.RemotePort,
		DstIP:   net.IP(id.LocalAddress),
		DstPort: id.LocalPort,
	}

	generateNATKey := func(m *M.Metadata) string {
		return m.SourceAddress() /* as Full Cone NAT Key */
	}
	key := generateNATKey(metadata)

	handle := func(drop bool) bool {
		pc := _natTable.Get(key)
		if pc != nil {
			dest := &net.UDPAddr{IP: metadata.DstIP, Port: int(metadata.DstPort)}
			handleUDPToRemote(packet, pc, dest, drop)
			return true
		}
		return false
	}

	if handle(true /* drop */) {
		return
	}

	lockKey := key + "-lock"
	cond, loaded := _natTable.GetOrCreateLock(lockKey)
	go func() {
		if loaded {
			cond.L.Lock()
			cond.Wait()
			handle(true) /* drop after sending data to remote */
			cond.L.Unlock()
			return
		}

		defer func() {
			_natTable.Delete(lockKey)
			cond.Broadcast()
		}()

		ctx := vsession.ContextWithID(context.Background(), vsession.NewID())
		ctx, cancel := context.WithCancel(ctx)
		pc, err := DialUDP(ctx, v)
		timer := vsignal.CancelAfterInactivity(ctx, cancel, _udpSessionTimeout)
		if err != nil {
			// log.Printf("[UDP] dial %s error: %v", metadata.DestinationAddress(), err)
			return
		}

		if dialerAddr, ok := pc.LocalAddr().(*net.UDPAddr); ok {
			metadata.MidIP = dialerAddr.IP
			metadata.MidPort = uint16(dialerAddr.Port)
		} else { /* fallback */
			metadata.MidIP, metadata.MidPort = parseAddr(pc.LocalAddr().String())
		}

		go func() {
			defer pc.Close()
			defer packet.Drop()
			defer _natTable.Delete(key)

			handleUDPToLocal(packet, pc, timer)
		}()

		_natTable.Set(key, pc)
		handle(false /* drop */)
	}()
}

func handleUDPToRemote(packet core.UDPPacket, pc net.PacketConn, remote net.Addr, drop bool) {
	defer func() {
		if drop {
			packet.Drop()
		}
	}()

	if _, err := pc.WriteTo(packet.Data() /* data */, remote); err != nil {
		// log.Printf("[UDP] write to %s error: %v", remote, err)
		return
	}
	pc.SetReadDeadline(time.Now().Add(_udpSessionTimeout)) /* reset timeout */

	// log.Printf("[UDP] %s --> %s", packet.RemoteAddr(), remote)
}

func handleUDPToLocal(packet core.UDPPacket, pc net.PacketConn, timer vsignal.ActivityUpdater) {
	buf := pool.Get(MaxSegmentSize)
	defer pool.Put(buf)

	for /* just loop */ {
		// pc.SetReadDeadline(time.Now().Add(_udpSessionTimeout))
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			// log.Printf("[UDP] read error: %v", err)
			return
		}
		timer.Update()

		if _, err := packet.WriteBack(buf[:n], from); err != nil {
			// log.Printf("[UDP] write back from %s error: %v", from, err)
			return
		}

		// log.Printf("[UDP] %s <-- %s recv:%d send:%d", packet.RemoteAddr(), from, n, size)
	}
}

func DialUDP(ctx context.Context, v *vcore.Instance) (net.PacketConn, error) {
	dispatcher := v.GetFeature(vrouting.DispatcherType())
	if dispatcher == nil {
		return nil, errors.New("routing.Dispatcher is not registered in V2Ray core")
	}
	return DialDispatcher(ctx, dispatcher.(vrouting.Dispatcher))
}

type dispatcherConn struct {
	dispatcher *vudptrans.Dispatcher
	cache      chan *udp.Packet
	done       *done.Instance
}

func DialDispatcher(ctx context.Context, dispatcher vrouting.Dispatcher) (net.PacketConn, error) {
	c := &dispatcherConn{
		cache: make(chan *udp.Packet, 64*1024),
		done:  done.New(),
	}

	// log.Printf("Dial udp with chan size 64*1024")
	d := vudptrans.NewDispatcher(dispatcher, c.callback)
	c.dispatcher = d
	return c, nil
}

func (c *dispatcherConn) callback(ctx context.Context, packet *udp.Packet) {
	select {
	case <-c.done.Wait():
		packet.Payload.Release()
		return
	case c.cache <- packet:
	default:
		log.Printf("udp chan full, drop")
		packet.Payload.Release()
	}
}

func (c *dispatcherConn) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case <-c.done.Wait():
		return 0, nil, io.EOF
	case packet := <-c.cache:
		n := copy(p, packet.Payload.Bytes())
		packet.Payload.Release()
		return n, &net.UDPAddr{
			IP:   packet.Source.Address.IP(),
			Port: int(packet.Source.Port),
		}, nil
	}
}

func (c *dispatcherConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	buffer := vbuf.New()
	raw := buffer.Extend(vbuf.Size)
	n := copy(raw, p)
	buffer.Resize(0, int32(n))

	ctx := context.Background()
	c.dispatcher.Dispatch(ctx, vnet.DestinationFromAddr(addr), buffer)
	return n, nil
}

func (c *dispatcherConn) Close() error {
	return c.done.Close()
}

func (c *dispatcherConn) LocalAddr() net.Addr {
	return &net.UDPAddr{
		IP:   []byte{0, 0, 0, 0},
		Port: 0,
	}
}

func (c *dispatcherConn) SetDeadline(t time.Time) error {
	return nil
}

func (c *dispatcherConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (c *dispatcherConn) SetWriteDeadline(t time.Time) error {
	return nil
}
