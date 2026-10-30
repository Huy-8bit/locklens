package queries

import _ "embed"

//go:embed tokens/insert_token.sql
var InsertToken string

//go:embed tokens/list_tokens.sql
var ListTokens string

//go:embed tokens/get_token_by_hash.sql
var GetTokenByHash string

//go:embed tokens/touch_token.sql
var TouchToken string

//go:embed tokens/revoke_token.sql
var RevokeToken string
