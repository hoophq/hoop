package admin

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hoophq/hoop/client/cmd/styles"
	"github.com/spf13/cobra"
)

var (
	eventPayloadFlag         string
	eventTypeDescriptionFlag string

	endpointUrlFlag         string
	endpointDescriptionFlag string
	endpointFilterTypesFlag []string

	messageEventTypeFlag string
	webhookOverwriteFlag bool
)

func init() {
	createSvixEventTypeCmd.Flags().StringVar(&eventPayloadFlag, "payload", "", "The path of the payload (file:///path/to/file.json) or the raw contents in json")
	createSvixEventTypeCmd.Flags().StringVar(&eventTypeDescriptionFlag, "description", "", "The description of the event type")
	createSvixEventTypeCmd.Flags().BoolVar(&webhookOverwriteFlag, "overwrite", false, "It will perform an update operation in the resource")

	createSvixEndppointCmd.Flags().StringVar(&endpointUrlFlag, "url", "", "The webhook endpoint url")
	createSvixEndppointCmd.Flags().StringVar(&endpointDescriptionFlag, "description", "", "The description of the event type resource")
	createSvixEndppointCmd.Flags().StringSliceVar(&endpointFilterTypesFlag, "filters", []string{}, "Which filter to type when sending messages to this endpoint")
	createSvixEndppointCmd.Flags().BoolVar(&webhookOverwriteFlag, "overwrite", false, "It will perform an update operation in the resource")

	createSvixMessageCmd.Flags().StringVar(&messageEventTypeFlag, "event-type", "", "The event type of the message")

}

var createSvixEventTypeCmd = &cobra.Command{
	Use:   "svixeventtype NAME",
	Short: "Create Svix Event Type",
	Example: `hoop admin create svixeventtype session.open --description 'session.open sent when a session starts'
hoop admin create svixet session.close --description --payload file:///path/to/session.close.schema.json`,
	Aliases: []string{"svixeventtypes", "svixevent-type", "svixevent-types", "svixet"},
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Usage()
			styles.PrintErrorAndExit("missing resource name")
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		args = []string{"svixeventtype", args[0]}
		method := "POST"
		actionName := "created"
		if webhookOverwriteFlag {
			actionName = "updated"
			method = "PUT"
		}

		var payloadMap map[string]any
		if strings.HasPrefix(eventPayloadFlag, "file://") {
			payload, err := getEnvValue(eventPayloadFlag)
			if err != nil {
				styles.PrintErrorAndExit("failed loading event payload: %v", err)
			}
			if err := json.Unmarshal([]byte(payload), &payloadMap); err != nil {
				styles.PrintErrorAndExit("failed decoding payload to map (from file): %v", err)
			}
		} else if eventPayloadFlag != "" {
			if err := json.Unmarshal([]byte(eventPayloadFlag), &payloadMap); err != nil {
				styles.PrintErrorAndExit("failed decoding payload to map: %v", err)
			}
		}
		apir := parseResourceOrDie(args, method, outputFlag)
		requestBody := map[string]any{
			"name":        apir.name,
			"description": eventTypeDescriptionFlag,
		}
		if len(payloadMap) > 0 {
			requestBody["schemas"] = map[string]any{
				"1": payloadMap,
			}
		}
		resp, err := httpBodyRequest(apir, method, requestBody)
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}
		if apir.decodeTo == "raw" {
			jsonData, _ := resp.([]byte)
			fmt.Println(string(jsonData))
			return
		}
		fmt.Printf("svix event type %v %v\n", apir.name, actionName)
	},
}

var createSvixEndppointCmd = &cobra.Command{
	Use:     "svixendpoint [ID]",
	Short:   "Create or Update Svix Endpoint.",
	Aliases: []string{"svixendpoints", "svixep"},
	Example: `hoop admin create svixendpoint --description 'My main endpoint' --url https://webhook-endpoint
hoop admin create svixep --url https://webhook-endpoint --filters session.open,session.close
hoop admin create svixep ep_2vY1R1AfRPOCHMeK6vLSmpeLKvs --overwrite --url https://webhook-endpoint
	`,
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 && webhookOverwriteFlag {
			cmd.Usage()
			styles.PrintErrorAndExit("missing endpoint id when updating resource")
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		newArgs := []string{"svixendpoint"}
		if len(args) > 0 {
			newArgs = append(newArgs, args[0])
		}
		method := "POST"
		actionName := "created"
		if webhookOverwriteFlag {
			actionName = "updated"
			method = "PUT"
		}
		apir := parseResourceOrDie(newArgs, method, outputFlag)
		requestBody := map[string]any{
			"url":         endpointUrlFlag,
			"version":     1,
			"description": endpointDescriptionFlag,
		}
		if len(endpointFilterTypesFlag) > 0 {
			requestBody["filterTypes"] = endpointFilterTypesFlag
		}
		resp, err := httpBodyRequest(apir, method, requestBody)
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}
		if apir.decodeTo == "raw" {
			jsonData, _ := resp.([]byte)
			fmt.Println(string(jsonData))
			return
		}
		if method == "PUT" {
			fmt.Printf("svix endpoint %v %v\n", apir.name, actionName)
			return
		}
		fmt.Println("svix endpoint created")
	},
}

var createSvixMessageCmd = &cobra.Command{
	Use:   "svixmessage (PAYLOAD|FILE_PAYLOAD)",
	Short: "Creates a new message and dispatches it to all of the endpoints",
	Example: `hoop admin create svixmessage '{"message": "my webhook message"}' --event-type session.open
hoop admin create svixmessage file:///tmp/payload.json --event-type session.open
	`,
	Aliases: []string{"svixmessages", "svixmsg"},
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Usage()
			styles.PrintErrorAndExit("missing payload")
		}
		if messageEventTypeFlag == "" {
			cmd.Usage()
			styles.PrintErrorAndExit("missing --event-type flag")
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		args = []string{"svixmessage", args[0]}
		apir := parseResourceOrDie(args, "POST", outputFlag)

		var payloadMap map[string]any
		if strings.HasPrefix(args[1], "file://") {
			payload, err := getEnvValue(args[1])
			if err != nil {
				styles.PrintErrorAndExit("failed loading event payload: %v", err)
			}
			if err := json.Unmarshal([]byte(payload), &payloadMap); err != nil {
				styles.PrintErrorAndExit("failed decoding payload to map (from file): %v", err)
			}
		} else {
			if err := json.Unmarshal([]byte(args[1]), &payloadMap); err != nil {
				styles.PrintErrorAndExit("failed decoding payload to map: %v", err)
			}
		}
		resp, err := httpBodyRequest(apir, "POST", map[string]any{
			"eventType": messageEventTypeFlag,
			"payload":   payloadMap,
		})
		if err != nil {
			styles.PrintErrorAndExit(err.Error())
		}
		if apir.decodeTo == "raw" {
			jsonData, _ := resp.([]byte)
			fmt.Println(string(jsonData))
			return
		}
		fmt.Printf("svix message sent\n")
	},
}
