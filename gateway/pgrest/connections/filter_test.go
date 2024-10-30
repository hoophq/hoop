package pgconnections

// func TestUrlEncodeOptions(t *testing.T) {
// 	for _, tt := range []struct {
// 		msg     string
// 		opts    []*ConnectionOption
// 		want    string
// 		wantErr string
// 	}{
// 		{
// 			msg: "it must be able to encode all options",
// 			opts: []*ConnectionOption{
// 				WithOption("type", "database"),
// 				WithOption("subtype", "postgres"),
// 				WithOption("managed_by", "hoopagent"),
// 				WithOption("tags", "prod,devops"),
// 			},
// 			want: "&type=eq.database&subtype=eq.postgres&managed_by=eq.hoopagent&tags=cs.{prod,devops}",
// 		},
// 		{
// 			msg: "it must be able to encode empty value options as a null query expression",
// 			opts: []*ConnectionOption{
// 				WithOption("type", "database"),
// 				WithOption("subtype", "postgres"),
// 				WithOption("managed_by", ""),
// 				WithOption("tags", "devops,team"),
// 			},
// 			want: "&type=eq.database&subtype=eq.postgres&managed_by=is.null&tags=cs.{devops,team}",
// 		},
// 		{
// 			msg: "it must ignore unknown options",
// 			opts: []*ConnectionOption{
// 				WithOption("unknown_option", "val"),
// 				WithOption("tags.fo.bar", "val"),
// 			},
// 			want: "",
// 		},
// 		{
// 			msg:     "it must error with invalid option values",
// 			opts:    []*ConnectionOption{WithOption("subtype", "value with space")},
// 			wantErr: ErrInvalidOptionVal.Error(),
// 		},
// 		{
// 			msg:     "it must error with invalid option values, special characteres",
// 			opts:    []*ConnectionOption{WithOption("subtype", "value&^%$#@")},
// 			wantErr: ErrInvalidOptionVal.Error(),
// 		},
// 		{
// 			msg:     "it must error when tag values has invalid option values",
// 			opts:    []*ConnectionOption{WithOption("tags", "foo,tag with space")},
// 			wantErr: ErrInvalidOptionVal.Error(),
// 		},
// 		{
// 			msg:     "it must error when tag values are empty",
// 			opts:    []*ConnectionOption{WithOption("tags", "foo,,,")},
// 			wantErr: ErrInvalidOptionVal.Error(),
// 		},
// 	} {
// 		t.Run(tt.msg, func(t *testing.T) {
// 			v, err := urlEncodeOptions(tt.opts)
// 			if err != nil {
// 				assert.EqualError(t, err, tt.wantErr)
// 			}
// 			assert.Equal(t, tt.want, v)
// 		})
// 	}

// }
