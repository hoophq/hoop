package log

import (
	"fmt"
	"os"
	"time"

	"go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"
)

// VerboseEncoder é um encoder customizado para formato verbose
type VerboseEncoder struct {
	cfg           zapcore.EncoderConfig
	useEmoji      bool
	useColor      bool
	sessionStarts map[string]time.Time
	storedFields  map[string]interface{}
	startTime     time.Time
}

// NewVerboseEncoder cria um encoder para formato verbose (human + timestamps)
func NewVerboseEncoder(cfg zapcore.EncoderConfig) zapcore.Encoder {
	return &VerboseEncoder{
		cfg:           cfg,
		useEmoji:      os.Getenv("NO_COLOR") == "",
		useColor:      os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb",
		sessionStarts: make(map[string]time.Time),
		storedFields:  make(map[string]interface{}),
		startTime:     time.Now(),
	}
}

func (v *VerboseEncoder) Clone() zapcore.Encoder {
	cloned := &VerboseEncoder{
		cfg:           v.cfg,
		useEmoji:      v.useEmoji,
		useColor:      v.useColor,
		sessionStarts: v.sessionStarts,
		storedFields:  make(map[string]interface{}),
		startTime:     v.startTime,
	}

	// Copia stored fields
	for k, val := range v.storedFields {
		cloned.storedFields[k] = val
	}

	return cloned
}

// Implementar métodos necessários do zapcore.Encoder para VerboseEncoder
func (v *VerboseEncoder) AddArray(key string, marshaler zapcore.ArrayMarshaler) error {
	return nil
}

func (v *VerboseEncoder) AddObject(key string, marshaler zapcore.ObjectMarshaler) error {
	return nil
}

func (v *VerboseEncoder) AddBinary(key string, value []byte) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddByteString(key string, value []byte) {
	v.storedFields[key] = string(value)
}

func (v *VerboseEncoder) AddBool(key string, value bool) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddComplex128(key string, value complex128) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddComplex64(key string, value complex64) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddDuration(key string, value time.Duration) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddFloat64(key string, value float64) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddFloat32(key string, value float32) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddInt(key string, value int) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddInt64(key string, value int64) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddInt32(key string, value int32) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddInt16(key string, value int16) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddInt8(key string, value int8) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddString(key, value string) {
	v.storedFields[key] = value
	if os.Getenv("DEBUG_ENCODER") == "true" {
		fmt.Fprintf(os.Stderr, "DEBUG: VerboseEncoder.AddString called: %s = %s\n", key, value)
	}
}

func (v *VerboseEncoder) AddTime(key string, value time.Time) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddUint(key string, value uint) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddUint64(key string, value uint64) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddUint32(key string, value uint32) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddUint16(key string, value uint16) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddUint8(key string, value uint8) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddUintptr(key string, value uintptr) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddReflected(key string, value interface{}) error {
	v.storedFields[key] = value
	if os.Getenv("DEBUG_ENCODER") == "true" {
		fmt.Fprintf(os.Stderr, "DEBUG: VerboseEncoder.AddReflected called: %s = %v\n", key, value)
	}
	return nil
}

func (v *VerboseEncoder) OpenNamespace(key string) {
}

func (v *VerboseEncoder) EncodeEntry(entry zapcore.Entry, fields []zapcore.Field) (*buffer.Buffer, error) {
	// Formata mensagem primeiro para verificar se deve ser suprimida
	msg := v.formatMessage(entry.Message, fields)

	// Se a mensagem for vazia (suprimida), retorna buffer vazio válido
	if msg == "" {
		// Retorna um buffer válido mas vazio
		line := buffer.NewPool().Get()
		return line, nil
	}

	line := buffer.NewPool().Get()

	// Timestamp sempre visível em verbose
	line.AppendString("[")
	line.AppendTime(entry.Time, "15:04:05.000")
	line.AppendString("] ")

	// Emoji ou prefix baseado no level (só para níveis que têm emoji)
	emoji := levelEmojis[entry.Level]
	if emoji != "" && v.useEmoji {
		line.AppendString(emoji + " ")
	}

	// Mensagem principal formatada
	if v.useColor {
		switch entry.Level {
		case zapcore.ErrorLevel, zapcore.FatalLevel:
			line.AppendString(colorRed)
		case zapcore.WarnLevel:
			line.AppendString(colorYellow)
		case zapcore.InfoLevel:
			// Info usa cor padrão do terminal (sem cor)
		case zapcore.DebugLevel:
			line.AppendString(colorGray)
		}
	}

	line.AppendString(msg)

	if v.useColor {
		line.AppendString(colorReset)
	}

	// Adiciona caller info se disponível (detalhes extras em verbose)
	if entry.Caller.Defined {
		line.AppendString(" [")
		line.AppendString(entry.Caller.TrimmedPath())
		line.AppendString("]")
	}

	// Nova linha
	line.AppendString("\n")

	return line, nil
}

func (v *VerboseEncoder) formatMessage(msg string, fields []zapcore.Field) string {
	// Debug
	if os.Getenv("DEBUG_ENCODER") == "true" {
		fmt.Fprintf(os.Stderr, "DEBUG: VerboseEncoder.formatMessage called with %d direct fields, %d stored fields\n", len(fields), len(v.storedFields))
		for i, field := range fields {
			fmt.Fprintf(os.Stderr, "  direct[%d] %s = %v\n", i, field.Key, encoderUtils.GetFieldStringValue(field))
		}
		for k, val := range v.storedFields {
			fmt.Fprintf(os.Stderr, "  stored[%s] = %v\n", k, val)
		}
	}

	// Usa a função compartilhada para construir o fieldMap
	fieldMap := encoderUtils.BuildFieldMap(v.storedFields, fields)

	// Usa a função compartilhada para formatação verbose
	return encoderUtils.FormatVerboseMessage(msg, fieldMap, v.useEmoji)
}

// Funções removidas - agora estão em encoder_utils.go
