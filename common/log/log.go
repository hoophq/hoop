package log

import (
	"io"
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
	LevelTrace = "TRACE"
	LevelDebug = "DEBUG"
	LevelInfo  = "INFO"
	LevelWarn  = "WARN"
	LevelError = "ERROR"
)

var (
	defaultLoggerSetLevel        = func(l zapcore.Level) {}
	LogEncoding           string = os.Getenv("LOG_ENCODING")

	zlog  = NewDefaultLogger(nil)
	sugar = zlog.Sugar()

	// aliases
	Printf  = sugar.Infof
	Println = sugar.Info

	Debug  = sugar.Debug
	Debugf = sugar.Debugf
	Infof  = sugar.Infof
	Info   = sugar.Info
	Warnf  = sugar.Warnf
	Warn   = sugar.Warn
	Error  = sugar.Error
	Errorf = sugar.Errorf
	Fatalf = sugar.Fatalf
	Fatal  = sugar.Fatal

	With         = sugar.With
	IsDebugLevel = zlog.Level() == zapcore.DebugLevel
)

func NewDefaultLogger(additionalWriterLogger io.Writer) *zap.Logger {
	if LogEncoding == "" {
		LogEncoding = "json"
	}
	logLevel := parseToAtomicLevel(os.Getenv("LOG_LEVEL"))
	stdoutSink, closeOut, err := zap.Open("stdout")
	if err != nil {
		log.Fatal(err)
	}
	stderrSink, _, err := zap.Open("stderr")
	if err != nil {
		closeOut()
		log.Fatal(err)
	}
	encoderConfig := zapcore.EncoderConfig{
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
	}
	core := zapcore.NewCore(zapcore.NewJSONEncoder(encoderConfig), stdoutSink, logLevel)
	if additionalWriterLogger != nil {
		core = zapcore.NewTee(
			zapcore.NewCore(zapcore.NewJSONEncoder(encoderConfig), stdoutSink, logLevel),
			zapcore.NewCore(zapcore.NewJSONEncoder(encoderConfig), zapcore.AddSync(additionalWriterLogger), logLevel),
		)
	}
	if LogEncoding == "console" {
		core = zapcore.NewCore(zapcore.NewConsoleEncoder(encoderConfig), stdoutSink, logLevel)
		if additionalWriterLogger != nil {
			core = zapcore.NewTee(
				zapcore.NewCore(zapcore.NewConsoleEncoder(encoderConfig), stdoutSink, logLevel),
				zapcore.NewCore(zapcore.NewConsoleEncoder(encoderConfig), zapcore.AddSync(additionalWriterLogger), logLevel),
			)
		}
	}
	defaultLoggerSetLevel = logLevel.SetLevel
	logger := zap.New(core,
		// sampler
		zap.WrapCore(func(core zapcore.Core) zapcore.Core {
			return zapcore.NewSamplerWithOptions(core, time.Second, 1000, 15)
		}),
		zap.ErrorOutput(stderrSink),
		zap.AddCaller(),
		zap.AddStacktrace(zapcore.ErrorLevel),
	)
	zap.ReplaceGlobals(logger)
	return logger
}

func parseToAtomicLevel(level string) zap.AtomicLevel {
	logLevel := zap.NewAtomicLevelAt(zapcore.InfoLevel)
	switch strings.ToUpper(level) {
	case LevelDebug, LevelTrace:
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
