package v2ray

import (
	"context"
	"net"
	"time"

	vcore "github.com/v2fly/v2ray-core/v4"
	vsession "github.com/v2fly/v2ray-core/v4/common/session"
	vsignal "github.com/v2fly/v2ray-core/v4/common/signal"

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
		pc, err := vcore.DialUDP(ctx, v)
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

		// log.Printf("[UDP] %s <-- %s", packet.RemoteAddr(), from)
	}
}
