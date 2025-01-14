package tun2socks

import (
	"errors"
	"fmt"

	"github.com/katalix/go-l2tp/config"
	"github.com/katalix/go-l2tp/l2tp"
)

type application struct {
	cfg     *config.Config
	l2tpCtx *l2tp.Context
}

var l2tpApp *application

func newApplication(cfg *config.Config) (app *application, err error) {
	app = &application{
		cfg:            cfg,
	}

	app.l2tpCtx, err = l2tp.NewContext(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create L2TP context: %v", err)
	}

	return app, nil
}

func (app *application) start() int {
	// Listen for L2TP events
	app.l2tpCtx.RegisterEventHandler(app)

	// Instantiate tunnels and sessions from the config file
	for _, tcfg := range app.cfg.config.Tunnels {

		// Only support l2tpv2/ppp
		if tcfg.Config.Version != l2tp.ProtocolVersion2 {
			level.Error(app.logger).Log(
				"message", "unsupported tunnel protocol version",
				"version", tcfg.Config.Version)
			return 1
		}

		tunl, err := app.l2tpCtx.NewDynamicTunnel(tcfg.Name, tcfg.Config)
		if err != nil {
			level.Error(app.logger).Log(
				"message", "failed to create tunnel",
				"tunnel_name", tcfg.Name,
				"error", err)
			return 1
		}

		for _, scfg := range tcfg.Sessions {
			_, err := tunl.NewSession(scfg.Name, scfg.Config)
			if err != nil {
				level.Error(app.logger).Log(
					"message", "failed to create session",
					"session_name", scfg.Name,
					"error", err)
				return 1
			}
		}
	}
}

func (app *application) stop() {
	go func() {
		app.l2tpCtx.Close()
	}()
}

func (app *application) HandleEvent(event interface{}) {
	switch ev := event.(type) {
	case *l2tp.TunnelUpEvent:
		// log

	case *l2tp.TunnelDownEvent:
		// log

	case *l2tp.SessionUpEvent:
		// log

	case *l2tp.SessionDownEvent:
		// log
	}
}

// StartL2tp
// connection handler for l2tp
func StartL2tp(
	packetFlow PacketFlow,
	vpnService VpnService,
	configBytes []byte) error {
	if packetFlow != nil {
		logger := nil
		l2tpConfig, err := config.LoadString(string(configBytes))
		if err != nil {
			return errors.New(fmt.Sprintf("failed to parse config: %v", err))
		}
		l2tpApp, err = newApplication(l2tpConfig)
		if err != nil {
			return errors.New(fmt.Sprintf("failed to create L2TP context: %v", err))
		}
		app.start()
		return nil
	}
	return errors.New("packetFlow is null")
}

// StopL2tp
func StopV2tp() {
	if l2tpApp != nil {
		l2tpApp.stop()
	}
}
