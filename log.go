package tun2socks

import (
	alog "github.com/v2fly/v2ray-core/v4/app/log"
	vcommon "github.com/v2fly/v2ray-core/v4/common"
	vcommonlog "github.com/v2fly/v2ray-core/v4/common/log"
)

type LogService interface {
	WriteLog(s string) error
}

type logWriter struct {
	logger *LogService
}

func (w *logWriter) Write(s string) error {
	(*w.logger).WriteLog(s)
	return nil
}

func (w *logWriter) Close() error {
	return nil
}

func createLogWriter(logService LogService) vcommonlog.WriterCreator {
	return func() vcommonlog.Writer {
		return &logWriter{
			logger: &logService,
		}
	}
}

func registerLogService(logService LogService) {
	if logService != nil {
		vcommon.Must(alog.RegisterHandlerCreator(alog.LogType_Console, func(lt alog.LogType,
			options alog.HandlerCreatorOptions) (vcommonlog.Handler, error) {
			return vcommonlog.NewLogger(createLogWriter(logService)), nil
		}))
	}
}
