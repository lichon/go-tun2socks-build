package v2ray

import (
	"context"
	"fmt"
	"io"
	"net"

	vcore "github.com/v2fly/v2ray-core/v4"
	vnet "github.com/v2fly/v2ray-core/v4/common/net"
	vsession "github.com/v2fly/v2ray-core/v4/common/session"

	"github.com/eycorsican/go-tun2socks/core"
	"github.com/v2fly/v2ray-core/v4/common/bytespool"
)

type tcpHandler struct {
	ctx context.Context
	v   *vcore.Instance
}

func (h *tcpHandler) relay(lhs net.Conn, rhs net.Conn) {
	go func() {
		buf := bytespool.Alloc(BufSize)
		io.CopyBuffer(rhs, lhs, buf)
		bytespool.Free(buf)
		lhs.Close()
		rhs.Close()
	}()
	buf := bytespool.Alloc(BufSize)
	io.CopyBuffer(lhs, rhs, buf)
	bytespool.Free(buf)
	lhs.Close()
	rhs.Close()
}

func NewTCPHandler(ctx context.Context, instance *vcore.Instance) core.TCPConnHandler {
	return &tcpHandler{
		ctx: ctx,
		v:   instance,
	}
}

func (h *tcpHandler) Handle(conn net.Conn, target *net.TCPAddr) error {
	dest := vnet.DestinationFromAddr(target)
	sid := vsession.NewID()
	ctx := vsession.ContextWithID(h.ctx, sid)
	c, err := vcore.Dial(ctx, h.v, dest)
	if err != nil {
		return fmt.Errorf("dial V proxy connection failed: %v", err)
	}
	go h.relay(conn, c)
	return nil
}
