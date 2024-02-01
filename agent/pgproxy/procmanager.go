package pgproxy

import (
	"fmt"
	"net"
	"time"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	"github.com/runopsio/hoop/common/pgtypes"
)

type procInfo struct {
	host      string
	port      string
	pid       uint32
	secretKey uint32

	removed bool
}

type processManager struct {
	memory.Store
	procListCh chan []*procInfo
}

var procManager *processManager

// It follows the principles of this doc: https://www.postgresql.org/docs/current/protocol-flow.html#PROTOCOL-FLOW-CANCELING-REQUESTS
func ProcManager() *processManager {
	if procManager != nil {
		return procManager
	}
	procManager = &processManager{
		Store:      memory.New(),
		procListCh: make(chan []*procInfo),
	}
	go func() {
		for procList := range procManager.procListCh {
			procManager.cancelRequest(procList)
		}
	}()
	return procManager
}

func (p *processManager) add(proc *procInfo) {
	objList := procManager.Get(proc.host)
	procInfoList, ok := objList.([]*procInfo)
	if ok {
		procInfoList = append(procInfoList, proc)
		p.Set(proc.host, procInfoList)
		return
	}
	p.Set(proc.host, []*procInfo{proc})
}

func (p *processManager) remove(host string, pid uint32) {
	obj := procManager.Get(host)
	procInfoList, ok := obj.([]*procInfo)
	if !ok || len(procInfoList) == 0 {
		return
	}
	for _, proc := range procInfoList {
		if proc.pid == pid {
			proc.removed = true
			break
		}
	}
	shouldKill := true
	for _, proc := range procInfoList {
		if !proc.removed {
			shouldKill = false
			break
		}
	}
	if shouldKill {
		select {
		case p.procListCh <- procInfoList:
		case <-time.After(time.Millisecond * 200):
		}
		procManager.Del(host)
		return
	}
	procManager.Set(host, procInfoList)
}

func (p *processManager) cancelRequest(procList []*procInfo) {
	if len(procList) == 0 {
		return
	}
	host, port := procList[0].host, procList[0].port
	pgConn, err := net.DialTimeout("tcp4", fmt.Sprintf("%s:%s", host, port), time.Second*3)
	if err != nil {
		log.With("pgproc").Warnf("fail to dial to %s:%s, reason=%v", host, port, err)
		return
	}
	defer pgConn.Close()
	errors := []string{}
	for _, proc := range procList {
		log.Infof("canceling request of pid=%v", proc.pid)
		untypedPkt := pgtypes.NewCancelRequestPacket(&pgtypes.BackendKeyData{Pid: proc.pid, SecretKey: proc.secretKey})
		if _, err := pgConn.Write(untypedPkt[:]); err != nil {
			errors = append(errors, err.Error())
		}
	}
	log.Infof("processed cancel request for total of %v, errors=%v, errorlist=%v",
		len(procList), len(errors), errors)
}
