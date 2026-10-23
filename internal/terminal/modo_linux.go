//go:build linux

package terminal

import (
	"syscall"
	"unsafe"
)

const (
	pedirModo   = syscall.TCGETS
	definirModo = syscall.TCSETS
)

type estado struct{ t syscall.Termios }

func obter(fd uintptr) (*estado, error) {
	var t syscall.Termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(pedirModo), uintptr(unsafe.Pointer(&t))); e != 0 {
		return nil, e
	}
	return &estado{t: t}, nil
}

func aplicar(fd uintptr, s *estado) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(definirModo), uintptr(unsafe.Pointer(&s.t))); e != 0 {
		return e
	}
	return nil
}

func cru(fd uintptr) (*estado, error) {
	antigo, err := obter(fd)
	if err != nil {
		return nil, err
	}
	n := *antigo
	n.t.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	n.t.Oflag |= syscall.OPOST
	n.t.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	n.t.Cflag &^= syscall.CSIZE | syscall.PARENB
	n.t.Cflag |= syscall.CS8
	n.t.Cc[syscall.VMIN] = 1
	n.t.Cc[syscall.VTIME] = 0
	if err := aplicar(fd, &n); err != nil {
		return nil, err
	}
	return antigo, nil
}

func restaurar(fd uintptr, s *estado) error { return aplicar(fd, s) }

func eTerminal(fd uintptr) bool {
	_, err := obter(fd)
	return err == nil
}

func largura(fd uintptr) int {
	var ws struct{ Linhas, Colunas, X, Y uint16 }
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws))); e != 0 || ws.Colunas == 0 {
		return 80
	}
	return int(ws.Colunas)
}

func prepararConsole() {}
