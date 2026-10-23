package saida

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

type Formato string

const (
	Tabela Formato = "tabela"
	JSON   Formato = "json"
	Texto  Formato = "texto"
)

func ParseFormato(s string) (Formato, error) {
	switch strings.ToLower(s) {
	case "", "tabela", "table":
		return Tabela, nil
	case "json":
		return JSON, nil
	case "texto", "text", "raw":
		return Texto, nil
	}
	return "", fmt.Errorf("formato desconhecido: %q (use tabela, json ou texto)", s)
}

type Grade struct {
	Colunas []string   `json:"colunas"`
	Linhas  [][]string `json:"linhas"`
	Direita []int      `json:"-"`
	Status  int        `json:"-"`
}

type Par struct {
	Rotulo string `json:"rotulo"`
	Valor  string `json:"valor"`
}

type Resultado struct {
	Titulo   string        `json:"titulo,omitempty"`
	Mensagem string        `json:"mensagem,omitempty"`
	Grade    *Grade        `json:"grade,omitempty"`
	Pares    []Par         `json:"pares,omitempty"`
	Bruto    []byte        `json:"-"`
	Texto    string        `json:"texto,omitempty"`
	Dados    any           `json:"dados,omitempty"`
	Rodape   string        `json:"rodape,omitempty"`
	Duracao  time.Duration `json:"duracao_ns,omitempty"`
}

type Estilo struct{ Cor bool }

func DetectarCor(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("LEME_SEM_COR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return Terminal(f)
}

func Terminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func (e Estilo) envolver(cod, s string) string {
	if !e.Cor || s == "" {
		return s
	}
	return "\x1b[" + cod + "m" + s + "\x1b[0m"
}

func (e Estilo) Negrito(s string) string  { return e.envolver("1", s) }
func (e Estilo) Fraco(s string) string    { return e.envolver("2", s) }
func (e Estilo) Verde(s string) string    { return e.envolver("32", s) }
func (e Estilo) Vermelho(s string) string { return e.envolver("31", s) }
func (e Estilo) Amarelo(s string) string  { return e.envolver("33", s) }

func (e Estilo) Ponto(estado string) string {
	switch estado {
	case "vivo", "ok":
		return e.Verde("●")
	case "suspeito", "aviso":
		return e.Amarelo("●")
	case "morto", "erro":
		return e.Vermelho("●")
	}
	return e.Fraco("○")
}

func Largura(s string) int {
	n := 0
	emEscape := false
	for _, r := range s {
		if r == '\x1b' {
			emEscape = true
			continue
		}
		if emEscape {
			if r == 'm' {
				emEscape = false
			}
			continue
		}
		n++
	}
	return n
}

func preencher(s string, w int, direita bool) string {
	falta := w - Largura(s)
	if falta <= 0 {
		return s
	}
	if direita {
		return strings.Repeat(" ", falta) + s
	}
	return s + strings.Repeat(" ", falta)
}

func Escrever(w io.Writer, r *Resultado, f Formato, e Estilo) error {
	if r == nil {
		return nil
	}
	switch f {
	case JSON:
		var v any = r.Dados
		if v == nil {
			m := map[string]any{}
			if r.Mensagem != "" {
				m["mensagem"] = r.Mensagem
			}
			if r.Grade != nil {
				var linhas []map[string]string
				for _, l := range r.Grade.Linhas {
					obj := map[string]string{}
					for i, c := range r.Grade.Colunas {
						if i < len(l) {
							obj[strings.ToLower(c)] = l[i]
						}
					}
					linhas = append(linhas, obj)
				}
				m["itens"] = linhas
			}
			if len(r.Pares) > 0 {
				for _, p := range r.Pares {
					m[strings.ToLower(strings.ReplaceAll(p.Rotulo, " ", "_"))] = p.Valor
				}
			}
			v = m
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(v)
	case Texto:
		if r.Bruto != nil {
			w.Write(r.Bruto)
			if len(r.Bruto) > 0 && r.Bruto[len(r.Bruto)-1] != '\n' {
				io.WriteString(w, "\n")
			}
			return nil
		}
		if r.Texto != "" {
			io.WriteString(w, strings.TrimRight(r.Texto, "\n")+"\n")
			return nil
		}
		if r.Grade != nil {
			for _, l := range r.Grade.Linhas {
				io.WriteString(w, strings.Join(l, "\t")+"\n")
			}
			return nil
		}
		for _, p := range r.Pares {
			fmt.Fprintf(w, "%s\t%s\n", p.Rotulo, p.Valor)
		}
		if r.Mensagem != "" && len(r.Pares) == 0 {
			io.WriteString(w, r.Mensagem+"\n")
		}
		return nil
	}
	if r.Titulo != "" {
		io.WriteString(w, e.Negrito(r.Titulo)+"\n")
	}
	if r.Bruto != nil && r.Grade == nil && len(r.Pares) == 0 {
		io.WriteString(w, Exibivel(r.Bruto)+"\n")
	}
	if r.Texto != "" && r.Bruto == nil && r.Mensagem == "" && r.Grade == nil && len(r.Pares) == 0 {
		io.WriteString(w, strings.TrimRight(r.Texto, "\n")+"\n")
	}
	if len(r.Pares) > 0 {
		larg := 0
		for _, p := range r.Pares {
			if l := Largura(p.Rotulo); l > larg {
				larg = l
			}
		}
		for _, p := range r.Pares {
			fmt.Fprintf(w, "%s  %s\n", e.Fraco(preencher(p.Rotulo, larg, false)), p.Valor)
		}
	}
	if r.Grade != nil {
		escreverGrade(w, r.Grade, e)
	}
	if r.Mensagem != "" {
		io.WriteString(w, r.Mensagem+"\n")
	}
	if r.Rodape != "" {
		io.WriteString(w, e.Fraco(r.Rodape)+"\n")
	}
	return nil
}

func escreverGrade(w io.Writer, g *Grade, e Estilo) {
	if len(g.Linhas) == 0 {
		io.WriteString(w, e.Fraco("(vazio)")+"\n")
		return
	}
	larg := make([]int, len(g.Colunas))
	for i, c := range g.Colunas {
		larg[i] = Largura(c)
	}
	for _, l := range g.Linhas {
		for i := range g.Colunas {
			if i < len(l) {
				if x := Largura(l[i]); x > larg[i] {
					larg[i] = x
				}
			}
		}
	}
	direita := map[int]bool{}
	for _, d := range g.Direita {
		direita[d] = true
	}
	var cab []string
	for i, c := range g.Colunas {
		cab = append(cab, preencher(c, larg[i], direita[i]))
	}
	io.WriteString(w, e.Fraco(strings.TrimRight(strings.Join(cab, "   "), " "))+"\n")
	for _, l := range g.Linhas {
		var cel []string
		for i := range g.Colunas {
			v := ""
			if i < len(l) {
				v = l[i]
			}
			cel = append(cel, preencher(v, larg[i], direita[i]))
		}
		io.WriteString(w, strings.TrimRight(strings.Join(cel, "   "), " ")+"\n")
	}
}

func Exibivel(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return fmt.Sprintf("<%d bytes binários>", len(b))
}

func Resumir(b []byte, max int) string {
	s := Exibivel(b)
	s = strings.ReplaceAll(s, "\n", "⏎")
	if utf8.RuneCountInString(s) > max {
		r := []rune(s)
		return string(r[:max-1]) + "…"
	}
	return s
}

func Bytes(n int64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(u), 0
	for m := n / u; m >= u; m /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func Duracao(d time.Duration) string {
	switch {
	case d <= 0:
		return "—"
	case d < time.Millisecond:
		return fmt.Sprintf("%dµs", d.Microseconds())
	case d < time.Second:
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
}
