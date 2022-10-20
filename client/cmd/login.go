package cmd

import (
	"bufio"
	"fmt"
	"github.com/spf13/cobra"
	"os"
	"strings"
)

// loginCmd represents the login command
var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate at Hoop",
	Long:  `Login to gain access to hoop usage.`,
	Run: func(cmd *cobra.Command, args []string) {
		doLogin(args)
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
	loginCmd.Flags().BoolP("email", "u", false, "The email used to authenticate at hoop")
}

func doLogin(args []string) {
	var email string
	if len(args) == 0 {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Email > ")
		email, _ = reader.ReadString('\n')
		email = strings.Trim(email, " \n")
	} else {
		email = args[0]
	}
	fmt.Printf("Logging in [%s]\n", email)

}
