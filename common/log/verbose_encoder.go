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
	*BaseEncoder  // Composição - herda todos os métodos Add*
	cfg           zapcore.EncoderConfig
	useEmoji      bool
	useColor      bool
	sessionStarts map[string]time.Time
	startTime     time.Time
}

// NewVerboseEncoder cria um encoder para formato verbose (human + timestamps)
func NewVerboseEncoder(cfg zapcore.EncoderConfig) zapcore.Encoder {
	return &VerboseEncoder{
		BaseEncoder:   NewBaseEncoder(),
		cfg:           cfg,
		useEmoji:      os.Getenv("NO_COLOR") == "",
		useColor:      os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb",
		sessionStarts: make(map[string]time.Time),
		startTime:     time.Now(),
	}
}

func (v *VerboseEncoder) Clone() zapcore.Encoder {
	cloned := &VerboseEncoder{
		BaseEncoder:   &BaseEncoder{},
		cfg:           v.cfg,
		useEmoji:      v.useEmoji,
		useColor:      v.useColor,
		sessionStarts: v.sessionStarts,
		startTime:     v.startTime,
	}

	// Copia fields usando o método do BaseEncoder
	cloned.SetStoredFields(v.CopyStoredFields())

	return cloned
}

// Métodos Add* removidos - agora herdados do BaseEncoder via composição

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
		storedFields := v.GetStoredFields()
		fmt.Fprintf(os.Stderr, "DEBUG: VerboseEncoder.formatMessage called with %d direct fields, %d stored fields\n", len(fields), len(storedFields))
		for i, field := range fields {
			fmt.Fprintf(os.Stderr, "  direct[%d] %s = %v\n", i, field.Key, GetFieldStringValue(field))
		}
		for k, val := range storedFields {
			fmt.Fprintf(os.Stderr, "  stored[%s] = %v\n", k, val)
		}
	}

	// Usa a função compartilhada para construir o fieldMap
	fieldMap := BuildFieldMap(v.GetStoredFields(), fields)

	// Usa a função compartilhada para formatação verbose
	return encoderUtils.FormatVerboseMessage(msg, fieldMap, v.useEmoji)
}
