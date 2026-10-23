package comandos

import (
	"errors"
	"strings"
)

var ErrAspas = errors.New("aspas sem fechamento")

func Tokenizar(linha string) ([]string, error) {
	var out []string
	var atual strings.Builder
	tem := false
	var aspas rune
	escape := false
	for _, r := range linha {
		switch {
		case escape:
			switch r {
			case 'n':
				atual.WriteRune('\n')
			case 't':
				atual.WriteRune('\t')
			default:
				atual.WriteRune(r)
			}
			escape = false
			tem = true
		case r == '\\' && aspas != '\'':
			escape = true
		case aspas != 0:
			if r == aspas {
				aspas = 0
			} else {
				atual.WriteRune(r)
			}
		case r == '"' || r == '\'':
			aspas = r
			tem = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if tem {
				out = append(out, atual.String())
				atual.Reset()
				tem = false
			}
		default:
			atual.WriteRune(r)
			tem = true
		}
	}
	if aspas != 0 || escape {
		return nil, ErrAspas
	}
	if tem {
		out = append(out, atual.String())
	}
	return out, nil
}
