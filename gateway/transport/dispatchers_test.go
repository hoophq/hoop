package transport

import (
	"testing"

	"github.com/hoophq/hoop/gateway/storagev2/types"
)

func TestDispatchOpenSession(t *testing.T) {
	// _, cancelFn := context.WithCancel(context.Background())
	state := newDispatcherState(nil)
	addDispatcherStateEntry("123", state)
	go func() { <-state.requestCh }() // noop receiver
	go func() { state.responseCh <- openSessionResponse{nil, nil} }()
	pkt, err := DispatchOpenSession(&types.Client{ID: "123"})
	if err != nil {
		t.Fatal("it must not return return error")
	}
	if pkt != nil {
		t.Error("it must not return a packet")
	}
}

func TestDispatchOpenSessionErr(t *testing.T) {
	for _, tt := range []struct {
		msg          string
		state        *dispatcherState
		stateID      string
		noopReceiver func(s *dispatcherState)
		errMsg       string
	}{
		{
			msg:          "it must return error when the state does not exists",
			stateID:      "state-test01",
			state:        newDispatcherState(func() {}),
			noopReceiver: func(s *dispatcherState) { go func() { <-s.requestCh }() },
			errMsg:       "proxy manager state state-test01 not found",
		},
		{
			msg:     "it must return reconnect error when request channel timeouts",
			stateID: "state-test02",
			state: func() *dispatcherState {
				state := newDispatcherState(func() {})
				addDispatcherStateEntry("state-test02", state)
				return state
			}(),
			errMsg: ErrForceReconnect.Error(),
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			if tt.noopReceiver != nil {
				tt.noopReceiver(tt.state)
			}
			go func() { tt.state.responseCh <- openSessionResponse{nil, nil} }()
			_, err := DispatchOpenSession(&types.Client{ID: tt.stateID})
			if err == nil {
				t.Fatal("want state error, got=nil")
			}
			if err.Error() != tt.errMsg {
				t.Errorf("want state error=%v, got=%v", tt.errMsg, err)
			}
			_ = DispatchDisconnect(&types.Client{ID: tt.stateID})
			if state := getDispatcherState(tt.stateID); state != nil {
				t.Errorf("expected empty state for=%s", tt.stateID)
			}
		})
	}
}
