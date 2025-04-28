package pgrest

import (
	"fmt"
	"time"
)

func (a *Agent) GetMeta(key string) (v string) {
	if len(a.Metadata) > 0 {
		if val, ok := a.Metadata[key]; ok {
			return val
		}
	}
	return
}

func (a Agent) String() string {
	return fmt.Sprintf("org=%v,name=%v,mode=%v,hostname=%v,platform=%v,version=%v,goversion=%v,kernel=%v",
		a.OrgID, a.Name, a.Mode, a.GetMeta("hostname"), a.GetMeta("platform"), a.GetMeta("version"), a.GetMeta("goversion"), a.GetMeta("kernel_version"))
}

func (s *ProxyManagerState) GetConnectedAt() (t time.Time) {
	t, _ = time.ParseInLocation("2006-01-02T15:04:05", s.ConnectedAt, time.UTC)
	return
}
