package apitypes

import "fmt"

func (a Agent) String() string {
	m := a.Metadata
	return fmt.Sprintf("org=%v,name=%v,mode=%v,hostname=%v,platform=%v,version=%v,goversion=%v",
		a.OrgID, a.Name, a.Mode, m.Hostname, m.Platform, m.Version, m.GoVersion)
}
