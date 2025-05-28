package runbooks

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/hoophq/hoop/client/cmd/styles"
	"github.com/hoophq/hoop/common/runbooks"
	"github.com/spf13/cobra"
)

var (
	parametersFlag []string
	noColorFlag    bool
)

func init() {
	lintCmd.Flags().StringSliceVarP(&parametersFlag, "parameter", "p", nil, "The parameter to use when parsing the runbook")
	lintCmd.Flags().BoolVar(&noColorFlag, "no-color", false, "Omit color in output")
	MainCmd.AddCommand(lintCmd)
}

var MainCmd = &cobra.Command{
	Use:          "runbooks",
	Short:        "Runbook utility commands",
	Aliases:      []string{"runbook"},
	Hidden:       false,
	SilenceUsage: false,
}

var exampleDesc = `hoop runbooks lint ./runbooks/myfile.runbook.py
hoop runbooks lint /root/another-file.runbook.py
hoop runbooks lint -p account_id=123 <<< '{{ .account_id | description "Account ID" }}'
`

var lintCmd = &cobra.Command{
	Use:          "lint (PATH | STDIN)",
	Short:        "Validate runbook files and provide error messages when syntax errors are detected.",
	SilenceUsage: false,
	Example:      exampleDesc,
	Run: func(cmd *cobra.Command, args []string) {
		info, err := os.Stdin.Stat()
		if err != nil {
			printErr(err.Error())
		}
		var content []byte
		isStdinInput := info.Mode()&os.ModeCharDevice == 0 || info.Size() > 0
		if isStdinInput {
			stdinPipe := os.NewFile(uintptr(syscall.Stdin), "/dev/stdin")
			reader := bufio.NewReader(stdinPipe)
			for {
				stdinInput, err := reader.ReadByte()
				if err != nil && err == io.EOF {
					break
				}
				content = append(content, stdinInput)
			}
			stdinPipe.Close()
		} else {
			if len(args) == 0 || len(args[0]) == 0 {
				printErr("missing file path or stdin containing the file contents")
			}
			content, err = os.ReadFile(args[0])
			if err != nil {
				printErr("unable to read file %v, reason=%v", args[0], err)
			}
		}
		tmpl, err := runbooks.Parse(string(content))
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed parsing runbook template, reason=%v\n", err)
			printErrContext(content, err)
			os.Exit(1)
		}

		// add a default value to input parameters if the attribute is required
		// and it contains a default value. The template engine requires the key attribute
		// is set to not fail
		inputParams := parseParameters()
		for key, config := range tmpl.Attributes() {
			if configParams, ok := config.(map[string]any); ok {
				var isRequired bool
				if requiredObj, ok := configParams["required"]; ok {
					isRequired, _ = requiredObj.(bool)
				}
				if defaultObj, ok := configParams["default"]; ok {
					defaultVal, _ := defaultObj.(string)
					if isRequired {
						if _, ok := inputParams[key]; !ok {
							inputParams[key] = defaultVal
						}
					}
				}
			}
		}

		out := bytes.NewBuffer([]byte{})
		if err := tmpl.Execute(out, inputParams); err != nil {
			fmt.Fprintf(os.Stderr, "failed parsing template parameters, reason=%v\n", err)
			printErrContext(content, err)
			os.Exit(1)
		}
		fmt.Println(out.String())
	},
}

func parseParameters() map[string]string {
	out := map[string]string{}
	for _, parameter := range parametersFlag {
		key, val, found := strings.Cut(parameter, "=")
		if !found {
			continue
		}
		out[key] = val
	}
	return out
}

func printErrContext(content []byte, err error) {
	errMsg := err.Error()
	if strings.HasPrefix(errMsg, "template: :") {
		no, _, _ := strings.Cut(errMsg[11:], ":")
		lineNumber, err := strconv.Atoi(strings.TrimSpace(no))
		if err == nil {
			i := 0
			var contents []string
			for _, line := range bytes.Split(content, []byte("\n")) {
				i++
				newLine := string(line)
				if lineNumber == i && !noColorFlag {
					newLine = styles.KeywordHighlight(string(line))
				}
				contents = append(contents, newLine)
			}

			beforeContext := contents[max(lineNumber-3, 0):lineNumber]
			afterContext := contents[lineNumber:min(lineNumber+3, len(contents))]
			contentContext := strings.Join(beforeContext, "\n") + "\n" + strings.Join(afterContext, "\n")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, contentContext)
		}
	}
}

func printErr(format string, v ...any) {
	fmt.Fprintln(os.Stderr, fmt.Sprintf(format, v...))
	os.Exit(1)
}
