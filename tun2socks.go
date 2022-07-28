package tun2socks

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"syscall"

	vcore "github.com/v2fly/v2ray-core/v4"
	verrors "github.com/v2fly/v2ray-core/v4/common/errors"
	vprotocol "github.com/v2fly/v2ray-core/v4/common/protocol"
	vinbound "github.com/v2fly/v2ray-core/v4/features/inbound"
	vstats "github.com/v2fly/v2ray-core/v4/features/stats"
	vproxy "github.com/v2fly/v2ray-core/v4/proxy"
	"github.com/v2fly/v2ray-core/v4/proxy/vless"
	vinternet "github.com/v2fly/v2ray-core/v4/transport/internet"

	"go-tun2socks-build/core/device/rwbased"
	"go-tun2socks-build/core/stack"
	"go-tun2socks-build/v2ray"
)

var localDNS = "223.5.5.5:53"
var err error
var ipStack *stack.Stack
var endpoint *rwbased.Endpoint
var v *vcore.Instance
var statsManager vstats.Manager
var isStopped = false

const (
	v2Asset = "v2ray.location.asset"
)

type errPathObjHolder struct{}

func init() {
	runtime.GOMAXPROCS(64)
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

func newError(values ...interface{}) *verrors.Error {
	return verrors.New(values...).WithPathObj(errPathObjHolder{})
}

// VpnService should be implemented in Java/Kotlin.
type VpnService interface {
	// Protect is just a proxy to the VpnService.protect() method.
	// See also: https://developer.android.com/reference/android/net/VpnService.html#protect(int)
	Protect(fd int) bool
}

// PacketFlow should be implemented in Java/Kotlin.
type PacketFlow interface {
	// WritePacket should writes packets to the TUN fd.
	Write(packet []byte) (int, error)
}

// Write IP packets to the lwIP stack. Call this function in the main loop of
// the VpnService in Java/Kotlin, which should reads packets from the TUN fd.
func InputPacket(data []byte) {
	if endpoint != nil {
		endpoint.DeliverNetworkPacket(data)
	}
}

type QuerySpeed interface {
	UpdateTraffic(up int64, down int64)
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

func getInbound(handler vinbound.Handler) (vproxy.Inbound, error) {
	gi, ok := handler.(vproxy.GetInbound)
	if !ok {
		return nil, newError("can't get inbound proxy from handler.")
	}
	return gi.GetInbound(), nil
}

func AddUser(uuid string, email string, tag string) error {
	if v == nil {
		return newError("v instance is not ready")
	}

	inboundManager := v.GetFeature(vinbound.ManagerType()).(vinbound.Manager)
	inboundHandler, err := inboundManager.GetHandler(nil, tag)
	if err != nil {
		return err
	}
	p, err := getInbound(inboundHandler)
	if err != nil {
		return err
	}
	um, ok := p.(vproxy.UserManager)
	if !ok {
		return newError("proxy is not a UserManager")
	}
	account := vless.Account{
		Id:         uuid,
		Flow:       "",
		Encryption: "none",
	}
	pAccount, err := account.AsAccount()
	if err != nil {
		return err
	}
	return um.AddUser(nil, &vprotocol.MemoryUser{
		Account: pAccount,
		Email:   email,
		Level:   0,
	})
}

func RemoveUser(email string, tag string) error {
	if v == nil {
		return newError("v instance is not ready")
	}

	inboundManager := v.GetFeature(vinbound.ManagerType()).(vinbound.Manager)
	inboundHandler, err := inboundManager.GetHandler(nil, tag)
	if err != nil {
		return err
	}
	p, err := getInbound(inboundHandler)
	if err != nil {
		return err
	}
	um, ok := p.(vproxy.UserManager)
	if !ok {
		return newError("proxy is not a UserManager")
	}
	return um.RemoveUser(nil, email)
}

// StartV2Ray sets up lwIP stack, starts a V2Ray instance and registers the instance as the
// connection handler for tun2socks.
func StartV2Ray(
	packetFlow PacketFlow,
	vpnService VpnService,
	logService LogService,
	configBytes []byte) error {
	if packetFlow != nil {
		// v2ray log
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
		// vinternet.RegisterListenerController(netCtlr)

		// Start the V2Ray instance.
		v, err = vcore.StartInstance("json", configBytes)
		if err != nil {
			logService.WriteLog(fmt.Sprintf("start V instance failed: %v", err))
			return err
		}

		if ipStack == nil {
			endpoint, _ = rwbased.New(packetFlow, 1500)
			ipStack, _ = stack.New(endpoint, v2ray.NewHandler(v), stack.WithDefault())
		}

		statsManager = v.GetFeature(vstats.ManagerType()).(vstats.Manager)
		isStopped = false
		logService.WriteLog(fmt.Sprintf("V2Ray %s started!", CheckVersion()))
		return nil
	}
	return errors.New("packetFlow is null")
}

// StopV2Ray stop v2ray
func StopV2Ray() {
	isStopped = true

	if ipStack != nil {
		ipStack.Close()
		ipStack = nil
	}
	if statsManager != nil {
		statsManager.Close()
		statsManager = nil
	}
	if v != nil {
		v.Close()
		v = nil
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

func CheckVersion() string {
	return vcore.Version()
}
