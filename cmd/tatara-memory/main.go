package main

import (
	"fmt"
	"os"

	"github.com/szymonrychu/tatara-memory/internal/version"
)

func main() {
	_, _ = fmt.Fprintf(os.Stdout, "tatara-memory %s (commit %s, built %s)\n",
		version.Version, version.Commit, version.Date)
}
