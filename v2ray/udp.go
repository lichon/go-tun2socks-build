package v2ray

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
	"log"

	vcore "github.com/v2fly/v2ray-core/v4"
	vsession "github.com/v2fly/v2ray-core/v4/common/session"
	vsignal "github.com/v2fly/v2ray-core/v4/common/signal"
	vtask "github.com/v2fly/v2ray-core/v4/common/task"
	"github.com/v2fly/v2ray-core/v4/common/bytespool"

	"github.com/eycorsican/go-tun2socks/core"
)

type udpConnEntry struct {
	conn net.PacketConn

	// `ReadFrom` method of PacketConn given by V2Ray
	// won't return the correct remote address, we treat
	// all data receive from V2Ray are coming from the
	// same remote host, i.e. the `target` that passed
	// to `Connect`.
	target *net.UDPAddr

	updater vsignal.ActivityUpdater
}

type udpHandler struct {
	sync.Mutex

	ctx     context.Context
	v       *vcore.Instance
	conns   map[core.UDPConn]*udpConnEntry
	timeout time.Duration // Maybe override by V2Ray local policies for some conns.
}

func (h *udpHandler) fetchInput(conn core.UDPConn) {
	h.Lock()
	c, ok := h.conns[conn]
	h.Unlock()
	if !ok {
		return
	}

	buf := bytespool.Alloc(BufSize)
	defer bytespool.Free(buf)

	for {
		n, _, err := c.conn.ReadFrom(buf)
		if err != nil && n <= 0 {
			h.Close(conn)
			conn.Close()
			return
		}
		c.updater.Update()
		_, err = conn.WriteFrom(buf[:n], c.target)
		if err != nil {
			h.Close(conn)
			conn.Close()
			return
		}
	}
}

func NewUDPHandler(ctx context.Context, instance *vcore.Instance, timeout time.Duration) core.UDPConnHandler {
	return &udpHandler{
		ctx:     ctx,
		v:       instance,
		conns:   make(map[core.UDPConn]*udpConnEntry, 16),
		timeout: timeout,
	}
}

func (h *udpHandler) Connect(conn core.UDPConn, target *net.UDPAddr) error {
	if target == nil {
		return errors.New("nil target is not allowed")
	}
	sid := vsession.NewID()
	ctx := vsession.ContextWithID(h.ctx, sid)
	ctx, cancel := context.WithCancel(ctx)
	pc, err := vcore.DialUDP(ctx, h.v)
	if err != nil {
		cancel()
		return fmt.Errorf("dial V proxy connection failed: %v", err)
	}
	timer := vsignal.CancelAfterInactivity(ctx, cancel, h.timeout)
	h.Lock()
	h.conns[conn] = &udpConnEntry{
		conn:    pc,
		target:  target,
		updater: timer,
	}
	h.Unlock()
	fetchTask := func() error {
		h.fetchInput(conn)
		return nil
	}
	go func() {
		if err := vtask.Run(ctx, fetchTask); err != nil {
			pc.Close()
		}
	}()
	log.Println("new proxy connection for target: %s:%s", target.Network(), target.String())
	return nil
}

func (h *udpHandler) ReceiveTo(conn core.UDPConn, data []byte, addr *net.UDPAddr) error {
	h.Lock()
	c, ok := h.conns[conn]
	h.Unlock()

	if ok {
		_, err := c.conn.WriteTo(data, addr)
		c.updater.Update()
		if err != nil {
			h.Close(conn)
			return fmt.Errorf("write remote failed: %v", err)
		}
		return nil
	} else {
		h.Close(conn)
		return fmt.Errorf("proxy connection %v->%v does not exists", conn.LocalAddr(), addr)
	}
}

func (h *udpHandler) Close(conn core.UDPConn) {
	h.Lock()
	defer h.Unlock()

	if c, found := h.conns[conn]; found {
		c.conn.Close()
	}
	delete(h.conns, conn)
}
