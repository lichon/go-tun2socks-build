package l2tp

import (
	"log"
	"runtime"

	"go-tun2socks-build/core"
)

const (
	// maxUDPQueueSize is the max number of UDP packets
	// could be buffered. if queue is full, upcoming packets
	// would be dropped util queue is ready again.
	maxUDPQueueSize = 1 << 9
)

var (
	_numUDPWorkers = max(runtime.NumCPU(), 4 /* at least 4 workers */)
)

var _ core.Handler = (*L2tpHandler)(nil)

type L2tpHandler struct {
	tcpQueue chan core.TCPConn
	udpQueue chan core.UDPPacket
}

func NewHandler() *L2tpHandler {
	h := new(L2tpHandler)
	h.tcpQueue = make(chan core.TCPConn)
	h.udpQueue = make(chan core.UDPPacket, maxUDPQueueSize)
	go process(h)
	return h
}

// Add adds tcpConn to tcpQueue.
func (h *L2tpHandler) Add(conn core.TCPConn) {
	h.tcpQueue <- conn
}

// AddPacket adds udpPacket to udpQueue.
func (h *L2tpHandler) AddPacket(packet core.UDPPacket) {
	select {
	case h.udpQueue <- packet:
	default:
		log.Printf("queue is currently full, packet will be dropped")
		packet.Drop()
	}
}

func process(h *L2tpHandler) {
	for i := 0; i < _numUDPWorkers; i++ {
		queue := h.udpQueue
		go func() {
			for packet := range queue {
			}
		}()
	}

	for conn := range h.tcpQueue {
		// go handleTCP(conn, h.v)
	}
}
