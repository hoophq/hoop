package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/hoophq/hoop/client/cmd/styles"
	"github.com/hoophq/hoop/common/clientconfig"
	"github.com/spf13/cobra"
)

var getContextsCmd = &cobra.Command{
	Use:     "get-contexts",
	Aliases: []string{"contexts", "ls"},
	Short:   "List Hoop profiles in the config file",
	Long: `List [profile <name>] sections in the client config file.
The selected profile is the account used by other commands.`,
	Example:      "hoop config get-contexts",
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		path := configPath()
		list, err := clientconfig.ListProfiles(path)
		if err != nil {
			styles.PrintErrorAndExit("%s", err.Error())
		}
		if len(list) == 0 {
			fmt.Printf("No profiles in %s.\n", path)
			fmt.Println("Add a [profile <name>] section, or run `hoop config set-context <name>`.")
			return
		}
		fmt.Printf("File: %s\n", path)
		rows := make([][]string, 0, len(list))
		for _, item := range list {
			mark := ""
			if item.Current {
				mark = "*"
			}
			rows = append(rows, []string{mark, item.Name, item.APIURL})
		}
		fmt.Println(styles.RenderTable([]string{" ", "NAME", "API URL"}, rows))
		if _, err := clientconfig.CurrentProfile(path); err != nil {
			fmt.Println(styles.Fainted("%s", err.Error()))
		}
		warnIfConfigOverride()
	},
}

var currentContextCmd = &cobra.Command{
	Use:          "current-context",
	Short:        "Print the active Hoop profile",
	Example:      "hoop config current-context",
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		name, err := clientconfig.CurrentProfile(configPath())
		if err != nil {
			styles.PrintErrorAndExit("%s", err.Error())
		}
		fmt.Println(name)
	},
}

var useContextCmd = &cobra.Command{
	Use:     "use-context <name>",
	Aliases: []string{"use"},
	Short:   "Switch the active profile in config.toml",
	Long: `Select a [profile <name>] section in the client config file.
The other profiles stay in that same file.`,
	Example:      "hoop config use-context profile2",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		item, err := clientconfig.UseProfile(configPath(), args[0])
		if err != nil {
			styles.PrintErrorAndExit("%s", err.Error())
		}
		fmt.Printf("Switched to profile %q (%s).\n", item.Name, item.APIURL)
		warnIfConfigOverride()
	},
}

var setContextCmd = &cobra.Command{
	Use:          "set-context <name>",
	Short:        "Save the active credentials as a profile in config.toml",
	Example:      "hoop config set-context profile1",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		item, err := clientconfig.SaveProfile(configPath(), args[0])
		if err != nil {
			styles.PrintErrorAndExit("%s", err.Error())
		}
		fmt.Printf("Saved profile %q (%s).\n", item.Name, item.APIURL)
	},
}

var deleteContextCmd = &cobra.Command{
	Use:   "delete-context <name>",
	Short: "Delete a profile from config.toml",
	Long: `Delete one [profile <name>] section from the client config file.
The active profile cannot be deleted. Switch away from it first.`,
	Example:      "hoop config delete-context old",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		if err := clientconfig.DeleteProfile(configPath(), name); err != nil {
			styles.PrintErrorAndExit("%s", err.Error())
		}
		fmt.Printf("Deleted profile %q.\n", name)
	},
}

func configPath() string {
	path, _, err := clientconfig.NewPath(clientconfig.ClientFile)
	if err != nil {
		styles.PrintErrorAndExit("%s", err.Error())
	}
	return path
}

func warnIfConfigOverride() {
	if override := strings.TrimSpace(os.Getenv(clientconfig.ConfigFileEnv)); override != "" {
		fmt.Printf("HOOP_CONFIG=%s overrides the default config file.\n", override)
	}
}
