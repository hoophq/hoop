package cmdutils

import "github.com/spf13/pflag"

// FlagAliases makes each old flag name in aliases parse as its new name.
// The old name stays accepted and is not shown in help.
func FlagAliases(fs *pflag.FlagSet, aliases map[string]string) {
	prev := fs.GetNormalizeFunc()
	fs.SetNormalizeFunc(func(f *pflag.FlagSet, name string) pflag.NormalizedName {
		if newName, ok := aliases[name]; ok {
			name = newName
		}
		return prev(f, name)
	})
}
