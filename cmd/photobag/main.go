// Command photobag manages a photo library stored in a single SQLite file.
package main

import (
	"os"

	"photobag/internal/cli"
)

func main() { os.Exit(cli.Execute()) }
