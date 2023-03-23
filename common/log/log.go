package log

import (
	"log"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	LevelDebug = "DEBUG"
	LevelInfo  = "INFO"
	LevelWarn  = "WARN"
	LevelError = "ERROR"
)

var zlog = NewDefaultLogger()

func Sync() error { return zlog.Sync() }

func NewDefaultLogger() *zap.Logger {
	logEncoding := os.Getenv("LOG_ENCODING")
	if logEncoding == "" {
		logEncoding = "json"
	}
	logLevel := zap.NewAtomicLevelAt(zapcore.InfoLevel)
	switch strings.ToUpper(os.Getenv("LOG_LEVEL")) {
	case LevelDebug:
		logLevel = zap.NewAtomicLevelAt(zapcore.DebugLevel)
	case LevelWarn:
		logLevel = zap.NewAtomicLevelAt(zapcore.WarnLevel)
	case LevelError:
		logLevel = zap.NewAtomicLevelAt(zapcore.ErrorLevel)
	}

	// zap.NewProduction()
	// zapcore.NewSamplerWithOptions(core, time.Second, 10, 5)
	loggerConfig := &zap.Config{
		Level:    logLevel,
		Encoding: logEncoding,
		EncoderConfig: zapcore.EncoderConfig{
			LevelKey:       "level",
			TimeKey:        "timestamp",
			NameKey:        "logger",
			CallerKey:      "logger",
			FunctionKey:    zapcore.OmitKey,
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.LowercaseLevelEncoder,
			EncodeTime:     zapcore.TimeEncoderOfLayout(time.RFC3339),
			EncodeDuration: zapcore.SecondsDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		},
		Sampling: &zap.SamplingConfig{
			Initial:    1000, // allow the first 1000 logs
			Thereafter: 15,   // take every 15th log afterwards
		},
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	logger, err := loggerConfig.Build()
	if err != nil {
		log.Fatal(err)
	}
	return logger
}

var (
	// aliases
	Printf  = zlog.Sugar().Infof
	Println = zlog.Sugar().Info

	Debugf = zlog.Sugar().Debugf
	Infof  = zlog.Sugar().Infof
	Warnf  = zlog.Sugar().Warnf
	Errorf = zlog.Sugar().Errorf
	Fatalf = zlog.Sugar().Fatalf
	Fatal  = zlog.Sugar().Fatal

	With = zlog.Sugar().With
)
