package pgconnections

// var (
// 	reSanitize, _       = regexp.Compile(`^[a-zA-Z0-9_]+(?:[-\.]?[a-zA-Z0-9_]+){1,128}$`)
// 	ErrInvalidOptionVal = errors.New("option values must contain between 1 and 127 alphanumeric characters, it may include (-), (_) or (.) characters")
// )

// type ConnectionOption struct {
// 	key string
// 	val string
// }

// var availableOptions = map[string]string{
// 	"type":       "string",
// 	"subtype":    "string",
// 	"managed_by": "string",
// 	"agent_id":   "string",
// 	"tags":       "array",
// }

// // WithOption specify a key pair of options to apply the filtering.
// // Each applied option is considered a logical operator AND
// func WithOption(key string, val string) *ConnectionOption {
// 	return &ConnectionOption{key: key, val: val}
// }

// func urlEncodeOptions(opts []*ConnectionOption) (string, error) {
// 	var outcome string
// 	for i, opt := range opts {
// 		optType, found := availableOptions[opt.key]
// 		if !found {
// 			continue
// 		}

// 		// if val is empty, query for null fields
// 		val := "is.null"
// 		if optType == "array" {
// 			var tagVals []string
// 			for _, tagVal := range strings.Split(opt.val, ",") {
// 				tagVal = strings.TrimSpace(tagVal)
// 				if !reSanitize.MatchString(tagVal) {
// 					return "", ErrInvalidOptionVal
// 				}
// 				tagVals = append(tagVals, tagVal)
// 			}
// 			if len(tagVals) > 0 {
// 				// contains
// 				val = "cs.{" + strings.Join(tagVals, ",") + "}"
// 			}
// 		} else if opt.val != "" {
// 			if !reSanitize.MatchString(opt.val) {
// 				return "", ErrInvalidOptionVal
// 			}
// 			val = fmt.Sprintf("eq.%v", opt.val)
// 		}
// 		if i == 0 {
// 			outcome = fmt.Sprintf("%s=%s", opt.key, val)
// 			continue
// 		}
// 		outcome += fmt.Sprintf("&%s=%s", opt.key, val)
// 	}
// 	if len(outcome) > 0 {
// 		return "&" + outcome, nil
// 	}
// 	return outcome, nil
// }
