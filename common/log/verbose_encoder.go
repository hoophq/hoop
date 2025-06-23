package log

import (
	"fmt"
	"os"
	"strings"
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

	// Timestamp no formato [HH:MM:SS] - sem milissegundos
	line.AppendString("[")
	line.AppendTime(entry.Time, "15:04:05")
	line.AppendString("] ")

	// Level em maiúsculas com cores
	levelStr := strings.ToUpper(entry.Level.String())
	if v.useColor {
		switch entry.Level {
		case zapcore.ErrorLevel, zapcore.FatalLevel:
			line.AppendString(colorRed + levelStr + colorReset)
		case zapcore.WarnLevel:
			line.AppendString(colorYellow + levelStr + colorReset)
		case zapcore.InfoLevel:
			line.AppendString(colorBlue + levelStr + colorReset)
		case zapcore.DebugLevel:
			line.AppendString(colorGray + levelStr + colorReset)
		default:
			line.AppendString(levelStr)
		}
	} else {
		line.AppendString(levelStr)
	}

	// Espaçamento para alinhar com a mensagem
	line.AppendString("  ")

	// Mensagem principal formatada (sem emojis no verbose)
	line.AppendString(msg)

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
