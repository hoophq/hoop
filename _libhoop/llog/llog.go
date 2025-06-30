package llog

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	mockLogger = zap.NewNop()
	sugar      = mockLogger.Sugar()

	Printf  = sugar.Infof
	Println = sugar.Info
	Debug   = sugar.Debug
	Debugf  = sugar.Debugf
	Infof   = sugar.Infof
	Info    = sugar.Info
	Warnf   = sugar.Warnf
	Warn    = sugar.Warn
	Error   = sugar.Error
	Errorf  = sugar.Errorf
	Fatalf  = sugar.Fatalf
	Fatal   = sugar.Fatal
	With    = sugar.With
)

func NewHumanEncoder(cfg zapcore.EncoderConfig) zapcore.Encoder {
	return zapcore.NewConsoleEncoder(cfg)
}

func NewVerboseEncoder(cfg zapcore.EncoderConfig) zapcore.Encoder {
	return zapcore.NewConsoleEncoder(cfg)
}

func ReinitializeLogger() {
}
