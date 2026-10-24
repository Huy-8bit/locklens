// Command locklens investigates PostgreSQL lock contention.
package main

import (
	"context"
	"os"

	"github.com/locklens/locklens/internal/cli"
)

func main() {
	os.Exit(cli.Main(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
