package pgproxy

import (
	"fmt"
	"net"
	"sync"
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
	doneRespCh chan bool
	mutex      sync.RWMutex
}

var procManager *processManager

// ProcManager is useful when clients are not disconnected properly
// it ensures all process started in a host are closed in the event of aburpt disconnections.
// It implements accordingly to the principles of this doc: https://www.postgresql.org/docs/current/protocol-flow.html#PROTOCOL-FLOW-CANCELING-REQUESTS
//
// This function is a singleton that controls the state in memory of processes.
// It's also concurrent safe.
func ProcManager() *processManager {
	if procManager != nil {
		return procManager
	}
	// We never close the channel once it starts
	// let it die when the agent ends. It shouldn't be a problem
	procManager = &processManager{
		Store:      memory.New(),
		procListCh: make(chan []*procInfo),
		doneRespCh: make(chan bool),
		mutex:      sync.RWMutex{},
	}
	go func() {
		for procList := range procManager.procListCh {
			procManager.cancelRequest(procList)
			// notify that the cancel request routine has ended to the caller
			select {
			case procManager.doneRespCh <- true:
			case <-time.After(time.Millisecond * 200):
			}
		}
	}()
	return procManager
}

// add a process in the memory
func (p *processManager) add(proc *procInfo) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	objList := procManager.Get(proc.host)
	procInfoList, ok := objList.([]*procInfo)
	if ok {
		procInfoList = append(procInfoList, proc)
		p.Set(proc.host, procInfoList)
		return
	}
	p.Set(proc.host, []*procInfo{proc})
}

// flush will mark a single process as removed.
// This method triggers the cancelation when all processes are marked as removed.
// The process will block of a max of 1 second waiting for a completion
// response, it is useful for cascade ending processes (e.g.: agent shutdown)
func (p *processManager) flush(host string, pid uint32) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
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
	// trigger when all processes from a host are closed
	if shouldKill {
		select {
		case p.procListCh <- procInfoList:
		case <-time.After(time.Millisecond * 200):
		}
		// wait 1 second for a response
		select {
		case <-p.doneRespCh:
		case <-time.After(time.Second * 1):
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
	errors := []string{}
	for _, proc := range procList {
		pgConn, err := net.DialTimeout("tcp4", fmt.Sprintf("%s:%s", proc.host, proc.port), time.Second*3)
		if err != nil {
			log.Warnf("fail to dial to %s:%s, reason=%v", proc.host, proc.port, err)
			return
		}
		log.Infof("canceling request for pid=%v", proc.pid)
		untypedPkt := pgtypes.NewCancelRequestPacket(&pgtypes.BackendKeyData{Pid: proc.pid, SecretKey: proc.secretKey})
		if _, err := pgConn.Write(untypedPkt[:]); err != nil {
			errors = append(errors, err.Error())
		}
		_ = pgConn.Close()
	}
	log.Infof("processed cancel request for total of %v process(es), errors=%v, errorlist=%v",
		len(procList), len(errors), errors)
}
