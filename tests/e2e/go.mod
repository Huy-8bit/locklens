module github.com/locklens/locklens/tests/e2e

go 1.26.0

require github.com/jackc/pgx/v5 v5.11.0

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/pganalyze/pg_query_go/v6 v6.2.5 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/protobuf v1.33.0 // indirect
)

require github.com/locklens/locklens v0.0.0

replace github.com/locklens/locklens => ../../backend
