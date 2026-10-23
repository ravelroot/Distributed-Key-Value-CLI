//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !windows

package terminal

import "errors"

type estado struct{}

func cru(fd uintptr) (*estado, error)       { return nil, errors.New("modo cru indisponível") }
func restaurar(fd uintptr, s *estado) error { return nil }
func eTerminal(fd uintptr) bool             { return false }
func largura(fd uintptr) int                { return 80 }
func prepararConsole()                      {}
