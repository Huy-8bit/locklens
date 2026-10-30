package queries

import _ "embed"

//go:embed settings/get_setting.sql
var GetSetting string

//go:embed settings/put_setting.sql
var PutSetting string
