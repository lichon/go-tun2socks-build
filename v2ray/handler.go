package v2ray

import (
	"log"
	"runtime"

	vcore "github.com/v2fly/v2ray-core/v4"

	"go-tun2socks-build/core"
)

const (
	// maxUDPQueueSize is the max number of UDP packets
	// could be buffered. if queue is full, upcoming packets
	// would be dropped util queue is ready again.
	maxUDPQueueSize = 1 << 9
	MaxSegmentSize  = (1 << 16) - 1
	RelayBufferSize = 20 * 1024
)

var (
	_numUDPWorkers = max(runtime.NumCPU(), 4 /* at least 4 workers */)
)

var _ core.Handler = (*VHandler)(nil)

type VHandler struct {
	v        *vcore.Instance
	tcpQueue chan core.TCPConn
	udpQueue chan core.UDPPacket
}

func NewHandler(v *vcore.Instance) *VHandler {
	h := new(VHandler)
	h.v = v
	h.tcpQueue = make(chan core.TCPConn)
	h.udpQueue = make(chan core.UDPPacket, maxUDPQueueSize)
	go process(h)
	return h
}

// Add adds tcpConn to tcpQueue.
func (h *VHandler) Add(conn core.TCPConn) {
	h.tcpQueue <- conn
}

// AddPacket adds udpPacket to udpQueue.
func (h *VHandler) AddPacket(packet core.UDPPacket) {
	select {
	case h.udpQueue <- packet:
	default:
		log.Printf("queue is currently full, packet will be dropped")
		packet.Drop()
	}
}

func process(h *VHandler) {
	for i := 0; i < _numUDPWorkers; i++ {
		queue := h.udpQueue
		go func() {
			for packet := range queue {
				handleUDP(packet, h.v)
			}
		}()
	}

	for conn := range h.tcpQueue {
		go handleTCP(conn, h.v)
	}
}
