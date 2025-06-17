package log

import (
	"fmt"
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

	// CORREÇÃO: With usando zap.Logger base para preservar fields corretamente
	With = func(args ...interface{}) *zap.SugaredLogger {
		if os.Getenv("DEBUG_ENCODER") == "true" {
			log.Printf("DEBUG: With called with args: %v", args)
		}

		// Converte args para fields do zap
		fields := make([]zap.Field, 0, len(args)/2)
		for i := 0; i < len(args); i += 2 {
			if i+1 < len(args) {
				key := fmt.Sprintf("%v", args[i])
				value := args[i+1]
				fields = append(fields, zap.Any(key, value))
			}
		}

		// Usa o logger base com fields e retorna sugar
		result := zlog.With(fields...).Sugar()

		if os.Getenv("DEBUG_ENCODER") == "true" {
			log.Printf("DEBUG: Returning sugar logger from With")
		}

		return result
	}
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

	// Seleciona o encoder baseado em LOG_ENCODING
	var encoder zapcore.Encoder
	switch LogEncoding {
	case "human":
		encoder = NewHumanEncoder(encoderConfig)
	case "verbose":
		encoder = NewVerboseEncoder(encoderConfig)
	case "console":
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	default:
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	}

	core := zapcore.NewCore(encoder, stdoutSink, logLevel)
	if additionalWriterLogger != nil {
		core = zapcore.NewTee(
			core,
			zapcore.NewCore(encoder, zapcore.AddSync(additionalWriterLogger), logLevel),
		)
	}

	defaultLoggerSetLevel = logLevel.SetLevel
	logger := zap.New(core,
		// sampler - removido temporariamente para debug
		// zap.WrapCore(func(core zapcore.Core) zapcore.Core {
		// 	return zapcore.NewSamplerWithOptions(core, time.Second, 1000, 15)
		// }),
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

// ReinitializeLogger permite reinicializar o logger após mudanças nas variáveis de ambiente
func ReinitializeLogger() {
	// Re-lê as variáveis de ambiente
	LogEncoding = os.Getenv("LOG_ENCODING")

	// Debug
	if os.Getenv("DEBUG_ENCODER") == "true" {
		log.Printf("DEBUG: Reinitializing logger with encoding: %s", LogEncoding)
	}

	// Recria o logger
	oldLogger := zlog
	zlog = NewDefaultLogger(nil)
	sugar = zlog.Sugar()

	// Re-atribui os aliases
	Printf = sugar.Infof
	Println = sugar.Info
	Debug = sugar.Debug
	Debugf = sugar.Debugf
	Infof = sugar.Infof
	Info = sugar.Info
	Warnf = sugar.Warnf
	Warn = sugar.Warn
	Error = sugar.Error
	Errorf = sugar.Errorf
	Fatalf = sugar.Fatalf
	Fatal = sugar.Fatal
	With = func(args ...interface{}) *zap.SugaredLogger {
		if os.Getenv("DEBUG_ENCODER") == "true" {
			log.Printf("DEBUG: With called with args: %v", args)
		}

		// Teste simples: usar o sugar.With original
		result := sugar.With(args...)

		if os.Getenv("DEBUG_ENCODER") == "true" {
			log.Printf("DEBUG: Returning sugar logger from With")
		}

		return result
	}
	IsDebugLevel = zlog.Level() == zapcore.DebugLevel

	// Debug - teste se With está funcionando
	if os.Getenv("DEBUG_ENCODER") == "true" {
		log.Printf("DEBUG: Logger reinitialized. Old logger: %p, New logger: %p", oldLogger, zlog)
		log.Printf("DEBUG: Testing With functionality...")
		testLogger := With("test_field", "test_value")
		testLogger.Info("test message with field")
	}
}
