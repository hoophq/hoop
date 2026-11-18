package ica

import "github.com/hoophq/hoop/sidecar/daemon"

func rawTCPEnabled(c *daemon.ICARawTCPConfig) bool { return c != nil && c.Enable }
