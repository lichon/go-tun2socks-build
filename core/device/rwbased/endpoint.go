// Package rwbased provides the implementation of io.ReadWriter
// based data-link layer endpoints.
package rwbased

import (
	"errors"
	"io"

	"inet.af/netstack/tcpip"
	"inet.af/netstack/tcpip/buffer"
	"inet.af/netstack/tcpip/header"
	"inet.af/netstack/tcpip/stack"
)

var _ stack.LinkEndpoint = (*Endpoint)(nil)

// Endpoint implements the interface of stack.LinkEndpoint from io.ReadWriter.
type Endpoint struct {
	// rw is the io.ReadWriter for reading and writing packets.
	rw io.Writer

	// mtu (maximum transmission unit) is the maximum size of a packet.
	mtu uint32

	dispatcher stack.NetworkDispatcher
}

// New returns stack.LinkEndpoint(.*Endpoint) and error.
func New(rw io.Writer, mtu uint32) (*Endpoint, error) {
	if mtu == 0 {
		return nil, errors.New("MTU size is zero")
	}

	if rw == nil {
		return nil, errors.New("RW interface is nil")
	}

	return &Endpoint{
		rw:  rw,
		mtu: mtu,
	}, nil
}

// Attach launches the goroutine that reads packets from io.ReadWriter and
// dispatches them via the provided dispatcher.
func (e *Endpoint) Attach(dispatcher stack.NetworkDispatcher) {
	// go e.dispatchLoop()
	e.dispatcher = dispatcher
}

// IsAttached implements stack.LinkEndpoint.IsAttached.
func (e *Endpoint) IsAttached() bool {
	return e.dispatcher != nil
}

func (e *Endpoint) DeliverNetworkPacket(data []byte) {
	if !e.IsAttached() {
		return
	}
	pkb := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Data: buffer.NewVectorisedView(len(data), []buffer.View{buffer.NewViewFromBytes(data)}),
	})
	e.dispatcher.DeliverNetworkPacket("", "", header.IPv4ProtocolNumber, pkb)
	pkb.DecRef()
}

func (e *Endpoint) writePacket(pkt *stack.PacketBuffer) tcpip.Error {
	vView := buffer.NewVectorisedView(pkt.Size(), pkt.Views())

	if _, err := e.rw.Write(vView.ToView()); err != nil {
		return &tcpip.ErrInvalidEndpointState{}
	}
	return nil
}

// WritePacket writes packet back into io.ReadWriter.
func (e *Endpoint) WritePacket(_ stack.RouteInfo, _ tcpip.NetworkProtocolNumber, pkt *stack.PacketBuffer) tcpip.Error {
	return e.writePacket(pkt)
}

// WritePackets writes packets back into io.ReadWriter.
func (e *Endpoint) WritePackets(_ stack.RouteInfo, pkts stack.PacketBufferList, _ tcpip.NetworkProtocolNumber) (int, tcpip.Error) {
	n := 0
	for pkt := pkts.Front(); pkt != nil; pkt = pkt.Next() {
		if err := e.writePacket(pkt); err != nil {
			break
		}
		n++
	}
	return n, nil
}

func (e *Endpoint) WriteRawPacket(packetBuffer *stack.PacketBuffer) tcpip.Error {
	return &tcpip.ErrNotSupported{}
}

// MTU implements stack.LinkEndpoint.MTU.
func (e *Endpoint) MTU() uint32 {
	return e.mtu
}

// Capabilities implements stack.LinkEndpoint.Capabilities.
func (e *Endpoint) Capabilities() stack.LinkEndpointCapabilities {
	return stack.CapabilityNone
}

// MaxHeaderLength returns the maximum size of the link layer header. Given it
// doesn't have a header, it just returns 0.
func (*Endpoint) MaxHeaderLength() uint16 {
	return 0
}

// LinkAddress returns the link address of this endpoint.
func (*Endpoint) LinkAddress() tcpip.LinkAddress {
	return ""
}

// ARPHardwareType implements stack.LinkEndpoint.ARPHardwareType.
func (*Endpoint) ARPHardwareType() header.ARPHardwareType {
	return header.ARPHardwareNone
}

// AddHeader implements stack.LinkEndpoint.AddHeader.
func (e *Endpoint) AddHeader(tcpip.LinkAddress, tcpip.LinkAddress, tcpip.NetworkProtocolNumber, *stack.PacketBuffer) {
}

// Wait implements stack.LinkEndpoint.Wait.
func (e *Endpoint) Wait() {}
