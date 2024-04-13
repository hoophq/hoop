package apiconnections

import (
	"slices"

	"github.com/runopsio/hoop/gateway/pgrest"
	pgplugins "github.com/runopsio/hoop/gateway/pgrest/plugins"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
)

func accessControlAllowed(ctx pgrest.Context) (func(connName string) bool, error) {
	p, err := pgplugins.New().FetchOne(ctx, plugintypes.PluginAccessControlName)
	if err != nil {
		return nil, err
	}
	if p == nil || ctx.IsAdmin() {
		return func(_ string) bool { return true }, nil
	}

	return func(connName string) bool {
		for _, c := range p.Connections {
			if c.Name == connName {
				for _, userGroup := range ctx.GetUserGroups() {
					if allow := slices.Contains(c.Config, userGroup); allow {
						return allow
					}
				}
				return false
			}
		}
		return false
	}, nil
}
