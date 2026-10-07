package models_test

import (
	"os"
	"testing"

	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
)

func TestMain(m *testing.M) { os.Exit(pglitetest.Main(m)) }
