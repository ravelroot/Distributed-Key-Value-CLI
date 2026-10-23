//go:build windows

package terminal

import (
	"syscall"
	"unsafe"
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
	procSetConsoleCP   = kernel32.NewProc("SetConsoleCP")
	procSetOutputCP    = kernel32.NewProc("SetConsoleOutputCP")
	procGetInfo        = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

const (
	entradaEco        = 0x0004
	entradaLinha      = 0x0002
	entradaProcessada = 0x0001
	entradaVT         = 0x0200
	saidaProcessada   = 0x0001
	saidaVT           = 0x0004
)

type estado struct{ modo uint32 }

func obter(fd uintptr) (*estado, error) {
	var m uint32
	r, _, e := procGetConsoleMode.Call(fd, uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return nil, e
	}
	return &estado{modo: m}, nil
}

func definir(fd uintptr, m uint32) error {
	r, _, e := procSetConsoleMode.Call(fd, uintptr(m))
	if r == 0 {
		return e
	}
	return nil
}

func cru(fd uintptr) (*estado, error) {
	antigo, err := obter(fd)
	if err != nil {
		return nil, err
	}
	m := antigo.modo &^ (entradaEco | entradaLinha | entradaProcessada)
	m |= entradaVT
	if err := definir(fd, m); err != nil {
		return nil, err
	}
	return antigo, nil
}

func restaurar(fd uintptr, s *estado) error { return definir(fd, s.modo) }

func eTerminal(fd uintptr) bool {
	_, err := obter(fd)
	return err == nil
}

func largura(fd uintptr) int {
	h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		return 80
	}
	var info struct {
		Tamanho, Cursor [2]int16
		Atributos       uint16
		Janela          [4]int16
		Maximo          [2]int16
	}
	r, _, _ := procGetInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 80
	}
	w := int(info.Janela[2]-info.Janela[0]) + 1
	if w <= 0 {
		return 80
	}
	return w
}

func prepararConsole() {
	procSetConsoleCP.Call(65001)
	procSetOutputCP.Call(65001)
	h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		return
	}
	if s, err := obter(uintptr(h)); err == nil {
		definir(uintptr(h), s.modo|saidaProcessada|saidaVT)
	}
}
