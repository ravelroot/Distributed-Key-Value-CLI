package terminal

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInterrompido = errors.New("interrompido")
)

type Completar func(linha []rune, pos int) (inicio int, opcoes []string)

type Editor struct {
	Entrada      *os.File
	Saida        io.Writer
	Historico    []string
	MaxHistorico int
	Completar    Completar
	leitor       *bufio.Reader
	cru          bool
}

func Novo(entrada *os.File, saida io.Writer) *Editor {
	prepararConsole()
	e := &Editor{Entrada: entrada, Saida: saida, MaxHistorico: 1000}
	e.cru = eTerminal(entrada.Fd())
	e.leitor = bufio.NewReader(entrada)
	return e
}

func (e *Editor) Interativo() bool { return e.cru }

func EhTerminal(f *os.File) bool { return eTerminal(f.Fd()) }

func Largura(f *os.File) int { return largura(f.Fd()) }

func (e *Editor) Adicionar(linha string) {
	linha = strings.TrimRight(linha, "\r\n")
	if strings.TrimSpace(linha) == "" {
		return
	}
	if n := len(e.Historico); n > 0 && e.Historico[n-1] == linha {
		return
	}
	e.Historico = append(e.Historico, linha)
	if e.MaxHistorico > 0 && len(e.Historico) > e.MaxHistorico {
		e.Historico = e.Historico[len(e.Historico)-e.MaxHistorico:]
	}
}

func (e *Editor) Ler(prompt string) (string, error) {
	if !e.cru {
		linha, err := e.leitor.ReadString('\n')
		if err != nil && (err != io.EOF || linha == "") {
			return "", err
		}
		return strings.TrimRight(linha, "\r\n"), nil
	}
	antigo, err := cru(e.Entrada.Fd())
	if err != nil {
		e.cru = false
		fmt.Fprint(e.Saida, prompt)
		return e.Ler(prompt)
	}
	defer restaurar(e.Entrada.Fd(), antigo)
	return e.editar(prompt)
}

type estadoLinha struct {
	buf      []rune
	pos      int
	hist     int
	rascunho []rune
}

func larguraVisivel(s string) int {
	n := 0
	esc := false
	for _, r := range s {
		if r == '\x1b' {
			esc = true
			continue
		}
		if esc {
			if r == 'm' {
				esc = false
			}
			continue
		}
		n++
	}
	return n
}

func (e *Editor) redesenhar(prompt string, l *estadoLinha) {
	var b strings.Builder
	b.WriteString("\r")
	b.WriteString(prompt)
	b.WriteString(string(l.buf))
	b.WriteString("\x1b[K")
	if volta := len(l.buf) - l.pos; volta > 0 {
		fmt.Fprintf(&b, "\x1b[%dD", volta)
	}
	io.WriteString(e.Saida, b.String())
}

func (e *Editor) lerRuna() (rune, error) {
	r, _, err := e.leitor.ReadRune()
	return r, err
}

const (
	teclaCima = -(iota + 1)
	teclaBaixo
	teclaDireita
	teclaEsquerda
	teclaInicio
	teclaFim
	teclaApagar
	teclaPalavraEsq
	teclaPalavraDir
	teclaDesconhecida
)

func (e *Editor) lerEscape() rune {
	r, err := e.lerRuna()
	if err != nil {
		return teclaDesconhecida
	}
	if r == 'b' {
		return teclaPalavraEsq
	}
	if r == 'f' {
		return teclaPalavraDir
	}
	if r != '[' && r != 'O' {
		return teclaDesconhecida
	}
	var seq []rune
	for {
		c, err := e.lerRuna()
		if err != nil {
			return teclaDesconhecida
		}
		seq = append(seq, c)
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '~' {
			break
		}
		if len(seq) > 8 {
			return teclaDesconhecida
		}
	}
	s := string(seq)
	switch s {
	case "A":
		return teclaCima
	case "B":
		return teclaBaixo
	case "C":
		return teclaDireita
	case "D":
		return teclaEsquerda
	case "H", "1~", "7~":
		return teclaInicio
	case "F", "4~", "8~":
		return teclaFim
	case "3~":
		return teclaApagar
	case "1;5D", "1;3D":
		return teclaPalavraEsq
	case "1;5C", "1;3C":
		return teclaPalavraDir
	}
	return teclaDesconhecida
}

func (e *Editor) editar(prompt string) (string, error) {
	l := &estadoLinha{hist: len(e.Historico)}
	e.redesenhar(prompt, l)
	for {
		r, err := e.lerRuna()
		if err != nil {
			return "", err
		}
		if r == 27 {
			r = e.lerEscape()
		}
		switch r {
		case '\r', '\n':
			io.WriteString(e.Saida, "\r\n")
			return string(l.buf), nil
		case 3:
			io.WriteString(e.Saida, "^C\r\n")
			return "", ErrInterrompido
		case 4:
			if len(l.buf) == 0 {
				io.WriteString(e.Saida, "\r\n")
				return "", io.EOF
			}
			if l.pos < len(l.buf) {
				l.buf = append(l.buf[:l.pos], l.buf[l.pos+1:]...)
			}
		case 127, 8:
			if l.pos > 0 {
				l.buf = append(l.buf[:l.pos-1], l.buf[l.pos:]...)
				l.pos--
			}
		case teclaApagar:
			if l.pos < len(l.buf) {
				l.buf = append(l.buf[:l.pos], l.buf[l.pos+1:]...)
			}
		case 1, teclaInicio:
			l.pos = 0
		case 5, teclaFim:
			l.pos = len(l.buf)
		case 2, teclaEsquerda:
			if l.pos > 0 {
				l.pos--
			}
		case 6, teclaDireita:
			if l.pos < len(l.buf) {
				l.pos++
			}
		case teclaPalavraEsq:
			l.pos = palavraAnterior(l.buf, l.pos)
		case teclaPalavraDir:
			l.pos = proximaPalavra(l.buf, l.pos)
		case 11:
			l.buf = l.buf[:l.pos]
		case 21:
			l.buf = append([]rune{}, l.buf[l.pos:]...)
			l.pos = 0
		case 23:
			ini := palavraAnterior(l.buf, l.pos)
			l.buf = append(l.buf[:ini], l.buf[l.pos:]...)
			l.pos = ini
		case 12:
			io.WriteString(e.Saida, "\x1b[H\x1b[2J")
		case 16, teclaCima:
			if l.hist > 0 {
				if l.hist == len(e.Historico) {
					l.rascunho = append([]rune{}, l.buf...)
				}
				l.hist--
				l.buf = []rune(e.Historico[l.hist])
				l.pos = len(l.buf)
			}
		case 14, teclaBaixo:
			if l.hist < len(e.Historico) {
				l.hist++
				if l.hist == len(e.Historico) {
					l.buf = append([]rune{}, l.rascunho...)
				} else {
					l.buf = []rune(e.Historico[l.hist])
				}
				l.pos = len(l.buf)
			}
		case '\t':
			e.completar(prompt, l)
		case teclaDesconhecida:
		default:
			if r >= 32 && utf8.ValidRune(r) {
				l.buf = append(l.buf, 0)
				copy(l.buf[l.pos+1:], l.buf[l.pos:])
				l.buf[l.pos] = r
				l.pos++
			}
		}
		e.redesenhar(prompt, l)
	}
}

func palavraAnterior(b []rune, pos int) int {
	i := pos
	for i > 0 && unicode.IsSpace(b[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(b[i-1]) {
		i--
	}
	return i
}

func proximaPalavra(b []rune, pos int) int {
	i := pos
	for i < len(b) && unicode.IsSpace(b[i]) {
		i++
	}
	for i < len(b) && !unicode.IsSpace(b[i]) {
		i++
	}
	return i
}

func PrefixoComum(opcoes []string) string {
	if len(opcoes) == 0 {
		return ""
	}
	p := opcoes[0]
	for _, o := range opcoes[1:] {
		for !strings.HasPrefix(o, p) {
			p = p[:len(p)-1]
		}
	}
	return p
}

func (e *Editor) completar(prompt string, l *estadoLinha) {
	if e.Completar == nil {
		return
	}
	ini, opcoes := e.Completar(l.buf, l.pos)
	if len(opcoes) == 0 || ini < 0 || ini > l.pos {
		return
	}
	atual := string(l.buf[ini:l.pos])
	var subst string
	if len(opcoes) == 1 {
		subst = opcoes[0] + " "
	} else {
		subst = PrefixoComum(opcoes)
		if len([]rune(subst)) <= len([]rune(atual)) {
			sort.Strings(opcoes)
			io.WriteString(e.Saida, "\r\n")
			largura := Largura(e.Entrada)
			col := 0
			for _, o := range opcoes {
				w := utf8.RuneCountInString(o) + 3
				if col+w > largura && col > 0 {
					io.WriteString(e.Saida, "\r\n")
					col = 0
				}
				io.WriteString(e.Saida, o+"   ")
				col += w
			}
			io.WriteString(e.Saida, "\r\n")
			return
		}
	}
	resto := append([]rune{}, l.buf[l.pos:]...)
	l.buf = append(append(append([]rune{}, l.buf[:ini]...), []rune(subst)...), resto...)
	l.pos = ini + len([]rune(subst))
}
