package whatsapp

import (
	"fmt"
	"log/slog"

	waLog "go.mau.fi/whatsmeow/util/log"
)

type slogLogger struct {
	logger *slog.Logger
	module string
}

func NewLogger(logger *slog.Logger) waLog.Logger {
	return &slogLogger{logger: logger, module: "whatsmeow"}
}

func (l *slogLogger) Warnf(message string, args ...any) {
	l.logger.Warn(fmt.Sprintf(message, args...), "module", l.module)
}

func (l *slogLogger) Errorf(message string, args ...any) {
	l.logger.Error(fmt.Sprintf(message, args...), "module", l.module)
}

func (l *slogLogger) Infof(message string, args ...any) {
	l.logger.Info(fmt.Sprintf(message, args...), "module", l.module)
}

func (l *slogLogger) Debugf(message string, args ...any) {
	l.logger.Debug(fmt.Sprintf(message, args...), "module", l.module)
}

func (l *slogLogger) Sub(module string) waLog.Logger {
	return &slogLogger{logger: l.logger, module: l.module + "/" + module}
}
