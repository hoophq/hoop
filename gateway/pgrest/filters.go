package pgrest

// type Filter interface {
// 	// Encode must return a valid encoded query string
// 	// to a postgrest table, view or rpc method.
// 	//
// 	// The first attribute must not contain the question mark symbol (?)
// 	Encode() string
// }

// type equalFilter struct {
// 	params map[string]string
// }

// type P map[string]string

// func (f *equalFilter) Encode() string {
// 	var v []string
// 	for key, val := range f.params {
// 		// for now, ignore empty values
// 		// it must deal with nulls and encode empty strings properly
// 		if val == "" {
// 			continue
// 		}
// 		v = append(v, fmt.Sprintf("%s=eq.%s", key, val))
// 	}
// 	if len(v) == 0 {
// 		return ""
// 	}
// 	return strings.Join(v, "&")
// }

// // WithEqFilter p is used to filter values vertically, multiple values are evaluated using AND by default.
// func WithEqFilter(values url.Values) *equalFilter {
// 	return &equalFilter{params: toFilterParams(values)}
// }

// // toFilterParams parses values into params.
// // If there are no values associated with the key, it skip
// // from filtering.
// //
// // It only considers the first value
// func toFilterParams(values url.Values) P {
// 	p := P{}
// 	for key, val := range values {
// 		if len(val) == 0 {
// 			continue
// 		}
// 		p[key] = val[0]
// 	}
// 	return p
// }
