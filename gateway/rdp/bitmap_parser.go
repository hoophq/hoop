package rdp

import (
	"context"
	"sync"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/rdp/parser"
)

var (
	globalParser     *parser.Parser
	globalParserOnce sync.Once
	globalParserErr  error
)

// InitParser initializes the global RDP bitmap parser
// It should be called once at application startup
func InitParser() error {
	globalParserOnce.Do(func() {
		ctx := context.Background()
		globalParser, globalParserErr = parser.NewParser(ctx)
		if globalParserErr != nil {
			log.Errorf("failed to initialize RDP parser: %v", globalParserErr)
		} else {
			log.Infof("RDP bitmap parser initialized successfully")
		}
	})
	return globalParserErr
}

// GetParser returns the global parser instance
func GetParser() *parser.Parser {
	return globalParser
}
