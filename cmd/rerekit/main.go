// Command rerekit records git conflict resolutions as committable,
// human-readable text files and replays them whenever the same conflict
// comes back — a shareable, reviewable alternative to git's rerere cache.
package main

import (
	"os"

	"github.com/JaydenCJ/rerekit/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
