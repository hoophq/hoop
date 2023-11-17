package pgrest

func (a *Agent) GetMeta(key string) (v string) {
	if len(a.Metadata) > 0 {
		if val, ok := a.Metadata[key]; ok {
			return val
		}
	}
	return
}
