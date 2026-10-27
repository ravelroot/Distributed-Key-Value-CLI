package versao

import (
	"fmt"
	"runtime"
)

var (
	Versao = "1.1.0"
	Commit = "dev"
	Data   = ""
)

const Nome = "Leme"

func Completa() string {
	s := fmt.Sprintf("%s %s (%s, %s/%s)", Nome, Versao, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	if Commit != "" && Commit != "dev" {
		s += " commit " + Commit
	}
	if Data != "" {
		s += " " + Data
	}
	return s
}
