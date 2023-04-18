package log

import (
	"log"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zapgrpc"
	"google.golang.org/grpc/grpclog"
)

const (
	LevelDebug = "DEBUG"
	LevelInfo  = "INFO"
	LevelWarn  = "WARN"
	LevelError = "ERROR"
)

var (
	zlog = NewDefaultLogger()

	// aliases
	Printf                = zlog.Sugar().Infof
	Println               = zlog.Sugar().Info
	defaultLoggerSetLevel = func(l zapcore.Level) {}

	Debugf = zlog.Sugar().Debugf
	Infof  = zlog.Sugar().Infof
	Info   = zlog.Sugar().Info
	Warnf  = zlog.Sugar().Warnf
	Error  = zlog.Sugar().Error
	Errorf = zlog.Sugar().Errorf
	Fatalf = zlog.Sugar().Fatalf
	Fatal  = zlog.Sugar().Fatal

	With                = zlog.Sugar().With
	IsDebugLevel        = zlog.Level() == zapcore.DebugLevel
	LogEncoding  string = os.Getenv("LOG_ENCODING")
)

func NewDefaultLogger() *zap.Logger {
	if LogEncoding == "" {
		LogEncoding = "json"
	}
	logLevel := parseToAtomicLevel(os.Getenv("LOG_LEVEL"))
	loggerConfig := &zap.Config{
		Level:    logLevel,
		Encoding: LogEncoding,
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
	defaultLoggerSetLevel = loggerConfig.Level.SetLevel
	logger, err := loggerConfig.Build()

	if err != nil {
		log.Fatal(err)
	}
	return logger
}

func parseToAtomicLevel(level string) zap.AtomicLevel {
	logLevel := zap.NewAtomicLevelAt(zapcore.InfoLevel)
	switch strings.ToUpper(level) {
	case LevelDebug:
		logLevel = zap.NewAtomicLevelAt(zapcore.DebugLevel)
	case LevelWarn:
		logLevel = zap.NewAtomicLevelAt(zapcore.WarnLevel)
	case LevelError:
		logLevel = zap.NewAtomicLevelAt(zapcore.ErrorLevel)
	}
	return logLevel
}

// SetGrpcLogger sets logger that is used in grpc.
// Not mutex-protected, should be called before any gRPC functions.
func SetGrpcLogger() {
	grpclog.SetLoggerV2(zapgrpc.NewLogger(zlog))
}

// SetDefaultLoggerLevel changes the default log level of the current logger
func SetDefaultLoggerLevel(level string) {
	if defaultLoggerSetLevel != nil {
		defaultLoggerSetLevel(parseToAtomicLevel(level).Level())
	}
}

func Sync() error { return zlog.Sync() }
