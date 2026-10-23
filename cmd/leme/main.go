package main

import (
	"os"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/cli"
)

func main() {
	os.Exit(cli.Executar(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
