package tun2socks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	vcore "github.com/v2fly/v2ray-core/v4"
	vproxyman "github.com/v2fly/v2ray-core/v4/app/proxyman"
	vbytespool "github.com/v2fly/v2ray-core/v4/common/bytespool"
	verrors "github.com/v2fly/v2ray-core/v4/common/errors"
	v2stats "github.com/v2fly/v2ray-core/v4/features/stats"
	vinternet "github.com/v2fly/v2ray-core/v4/transport/internet"

	"go-tun2socks-build/features"
	"go-tun2socks-build/ping"
	"go-tun2socks-build/pool"
	"go-tun2socks-build/runner"
	"go-tun2socks-build/v2ray"

	"github.com/eycorsican/go-tun2socks/core"
)

var localDNS = "223.5.5.5:53"
var err error
var lwipStack core.LWIPStack
var v *vcore.Instance
var mtuUsed int
var lwipTUNDataPipeTask *runner.Task
var updateStatusPipeTask *runner.Task
var tunDev *pool.Interface
var lwipWriter io.Writer
var statsManager v2stats.Manager
var isStopped = false

const (
	v2Asset = "v2ray.location.asset"
)

type errPathObjHolder struct{}

const (
	VMESS string = "vmess"
	VLESS string = "vless"
)

func newError(values ...interface{}) *verrors.Error {
	return verrors.New(values...).WithPathObj(errPathObjHolder{})
}

type VmessOptions features.VmessOptions
type Vmess features.Vmess

// VpnService should be implemented in Java/Kotlin.
type VpnService interface {
	// Protect is just a proxy to the VpnService.protect() method.
	// See also: https://developer.android.com/reference/android/net/VpnService.html#protect(int)
	Protect(fd int) bool
}

// PacketFlow should be implemented in Java/Kotlin.
type PacketFlow interface {
	// WritePacket should writes packets to the TUN fd.
	WritePacket(packet []byte)
}

// Write IP packets to the lwIP stack. Call this function in the main loop of
// the VpnService in Java/Kotlin, which should reads packets from the TUN fd.
func InputPacket(data []byte) {
	if lwipStack != nil {
		lwipStack.Write(data)
	}
}

type QuerySpeed interface {
	UpdateTraffic(up int64, down int64)
}

type TestLatency interface {
	UpdateLatency(id int, elapsed int64)
}

// SetNonblock puts the fd in blocking or non-blocking mode.
func SetNonblock(fd int, nonblocking bool) bool {
	err := syscall.SetNonblock(fd, nonblocking)
	if err != nil {
		return false
	}
	return true
}

// SetLocalDNS sets the DNS server that used by Go's default resolver, it accepts
// string in the form "host:port", e.g. 223.5.5.5:53
func SetLocalDNS(dns string) {
	localDNS = dns
}

// StartV2Ray sets up lwIP stack, starts a V2Ray instance and registers the instance as the
// connection handler for tun2socks.
func StartV2Ray(
	packetFlow PacketFlow,
	vpnService VpnService,
	logService LogService,
	querySpeed QuerySpeed,
	configBytes []byte,
	assetPath string) error {
	if packetFlow != nil {

		if lwipStack == nil {
			// Setup the lwIP stack.
			lwipStack = core.NewLWIPStack()
		}

		// Assets
		os.Setenv(v2Asset, assetPath)
		// log
		registerLogService(logService)

		// Protect file descriptors of net connections in the VPN process to prevent infinite loop.
		protectFd := func(s VpnService, fd int) error {
			if s.Protect(fd) {
				return nil
			} else {
				return errors.New(fmt.Sprintf("failed to protect fd %v", fd))
			}
		}
		netCtlr := func(network, address string, fd uintptr) error {
			return protectFd(vpnService, int(fd))
		}
		vinternet.RegisterDialerController(netCtlr)
		vinternet.RegisterListenerController(netCtlr)

		// Share the buffer pool.
		core.SetBufferPool(vbytespool.GetPool(core.BufSize))

		// Start the V2Ray instance.
		v, err = vcore.StartInstance("json", configBytes)
		if err != nil {
			log.Fatalf("start V instance failed: %v", err)
			return err
		}

		// Configure sniffing settings for traffic coming from tun2socks.
		sniffingConfig := &vproxyman.SniffingConfig{
			Enabled:             false,
			DestinationOverride: strings.Split("tls,http", ","),
		}
		ctx := contextWithSniffingConfig(context.Background(), sniffingConfig)

		// Register tun2socks connection handlers.
		// vhandler := v2ray.NewHandler(ctx, v)
		// core.RegisterTCPConnectionHandler(vhandler)
		// core.RegisterUDPConnectionHandler(vhandler)
		core.RegisterTCPConnHandler(v2ray.NewTCPHandler(ctx, v))
		core.RegisterUDPConnHandler(v2ray.NewUDPHandler(ctx, v, 3*time.Minute))

		// Write IP packets back to TUN.
		core.RegisterOutputFn(func(data []byte) (int, error) {
			if !isStopped {
				packetFlow.WritePacket(data)
			}
			return len(data), nil
		})

		statsManager = v.GetFeature(v2stats.ManagerType()).(v2stats.Manager)
		// runner.CheckAndStop(updateStatusPipeTask)
		// updateStatusPipeTask = createUpdateStatusPipeTask(querySpeed)
		isStopped = false
		logService.WriteLog(fmt.Sprintf("V2Ray %s started!", CheckVersion()))
		return nil
	}
	return errors.New("packetFlow is null")
}

func handlePacket(ctx context.Context, tunDev *pool.Interface, lwipWriter io.Writer, shouldStop runner.S) {
	// inbound := make(chan []byte, 100)
	// outbound := make(chan []byte, 1000)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// defer close(outbound)

	// writer
	go func(ctx context.Context) {
		for {
			select {
			case buffer, ok := <-tunDev.ReadCh:
				if !ok {
					return
				}
				_, _ = lwipWriter.Write(buffer)
				vbytespool.Free(buffer)
			case <-ctx.Done():
				return
			}
		}
	}(ctx)
	tunDev.Run(ctx)
}

func createUpdateStatusPipeTask(querySpeed QuerySpeed) *runner.Task {
	return runner.Go(func(shouldStop runner.S) error {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		zeroErr := errors.New("nil")
		for {
			if shouldStop() {
				break
			}
			select {
			case <-ticker.C:
				up := QueryOutboundStats("proxy", "uplink")
				down := QueryOutboundStats("proxy", "downlink")
				querySpeed.UpdateTraffic(up, down)
				// case <-lwipTUNDataPipeTask.StopChan():
				// 	return errors.New("stopped")
			}
		}
		return zeroErr
	})
}

// StopV2Ray stop v2ray
func StopV2Ray() {
	isStopped = true
	if tunDev != nil {
		tunDev.Stop()
	}
	runner.CheckAndStop(updateStatusPipeTask)
	runner.CheckAndStop(lwipTUNDataPipeTask)

	if lwipStack != nil {
		//lwipStack.Close()
		//lwipStack = nil
	}
	if statsManager != nil {
		statsManager.Close()
		statsManager = nil
	}
	if v != nil {
		v.Close()
		v = nil

		core.RegisterTCPConnHandler(nil)
		core.RegisterUDPConnHandler(nil)
	}
}

// ~/go/src/github.com/v2fly/v2ray-core/v4/proxy/vmess/outbound/outbound.go
func QueryStats(name string) int64 {
	if statsManager == nil {
		return 0
	}
	// name := "user>>>" + "xxf098@github.com" + ">>>traffic>>>" + direct + "link"
	counter := statsManager.GetCounter(name)
	if counter == nil {
		return 0
	}
	return counter.Set(0)
}

// add in v2ray-core v4.26.0
func QueryOutboundStats(tag string, direct string) int64 {
	if statsManager == nil {
		return 0
	}
	counter := statsManager.GetCounter(fmt.Sprintf("outbound>>>%s>>>traffic>>>%s", tag, direct))
	if counter == nil {
		return 0
	}
	return counter.Set(0)
}

func QueryInboundStats(tag string, direct string) int64 {
	if statsManager == nil {
		return 0
	}
	counter := statsManager.GetCounter(fmt.Sprintf("inbound>>>%s>>>traffic>>>%s", tag, direct))
	if counter == nil {
		return 0
	}
	return counter.Set(0)
}

// func queryStatsBg(log LogService) {
// 	for {
// 		if statsManager == nil {
// 			log.WriteLog("statsManager nil")
// 			return
// 		}
// 		name := "vmess>>>" + "ssrray" + ">>>traffic>>>" + "down"
// 		counter := statsManager.GetCounter(name)
// 		if counter == nil {
// 			log.WriteLog("counter nil")
// 		}
// 		time.Sleep(500 * time.Millisecond)
// 	}
// }

func init() {
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := net.Dialer{}
			return d.DialContext(ctx, "udp", localDNS)
			// d, _ := vnet.ParseDestination(fmt.Sprintf("%v:%v", network, localDNS))
			// return vinternet.DialSystem(ctx, d, nil)
		},
	}
}

func CheckVersion() string {
	return vcore.Version()
}

func TestURLLatency(url string) (int64, error) {
	return testLatency(url)
}

func TestTCPPing(host string, port int) (int64, error) {
	tcpping := ping.NewTCPPing(host, port)
	result := <-tcpping.Start()
	return result.Get()
}

func GetFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
