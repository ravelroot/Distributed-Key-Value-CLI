package comandos

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/cliente"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/protocolo"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/saida"
)

type TipoFlag int

const (
	FlagTexto TipoFlag = iota
	FlagInteiro
	FlagDuracao
	FlagBooleano
)

type Flag struct {
	Nome   string
	Curta  string
	Ajuda  string
	Tipo   TipoFlag
	Padrao string
}

type Args struct {
	Pos        []string
	Flags      map[string]string
	Entrada    io.Reader
	Interativo bool
}

type Sessao struct {
	Cliente      *cliente.Cliente
	Consistencia protocolo.Consistencia
	Estilo       saida.Estilo
}

type Comando struct {
	Nome      string
	Pai       string
	Apelidos  []string
	Uso       string
	Resumo    string
	Descricao string
	Exemplos  []string
	Grupo     string
	Min       int
	Max       int
	Flags     []Flag
	Perigoso  bool
	Exec      func(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error)
}

func (c *Comando) Caminho() string {
	if c.Pai != "" {
		return c.Pai + " " + c.Nome
	}
	return c.Nome
}

func (c *Comando) Assinatura() string {
	s := c.Caminho()
	if c.Uso != "" {
		s += " " + c.Uso
	}
	return s
}

type Grupo struct {
	Nome     string
	Apelidos []string
	Resumo   string
}

var Grupos = []Grupo{
	{Nome: "cluster", Resumo: "Estado, nós e anel do cluster"},
	{Nome: "node", Apelidos: []string{"no"}, Resumo: "Estatísticas, compactação e saída de um nó"},
}

var Ordem = []string{"Dados", "Cluster", "Nós"}

var Registro []*Comando

func registrar(c *Comando) { Registro = append(Registro, c) }

var flagConsistencia = Flag{Nome: "consistencia", Curta: "c", Ajuda: "um, quorum ou todos (padrão do perfil)", Tipo: FlagTexto}

func (a Args) tem(nome string) bool {
	_, ok := a.Flags[nome]
	return ok
}

func (a Args) Texto(nome string) string { return a.Flags[nome] }

func (a Args) Inteiro(nome string, padrao int) (int, error) {
	v, ok := a.Flags[nome]
	if !ok || v == "" {
		return padrao, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, Uso("--%s espera um número inteiro, recebeu %q", nome, v)
	}
	return n, nil
}

func (a Args) Uint(nome string) (uint64, error) {
	v, ok := a.Flags[nome]
	if !ok || v == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, Uso("--%s espera um número inteiro positivo, recebeu %q", nome, v)
	}
	return n, nil
}

func (a Args) Duracao(nome string) (time.Duration, error) {
	v, ok := a.Flags[nome]
	if !ok || v == "" {
		return 0, nil
	}
	return ParseDuracao(v)
}

func (a Args) Bool(nome string) bool {
	v, ok := a.Flags[nome]
	if !ok {
		return false
	}
	return v == "" || v == "true" || v == "1" || v == "sim"
}

func ParseDuracao(v string) (time.Duration, error) {
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, Uso("duração inválida %q (exemplos: 30s, 15m, 2h ou 90)", v)
	}
	return d, nil
}

func (a Args) consistencia(s *Sessao) (protocolo.Consistencia, error) {
	v := a.Texto("consistencia")
	if v == "" {
		return s.Consistencia, nil
	}
	c, err := protocolo.ParseConsistencia(v)
	if err != nil {
		return 0, Uso("%v", err)
	}
	return c, nil
}

type ErroUso struct{ Msg string }

func (e *ErroUso) Error() string { return e.Msg }

func Uso(formato string, args ...any) error { return &ErroUso{Msg: fmt.Sprintf(formato, args...)} }

const (
	SaidaOK            = 0
	SaidaErro          = 1
	SaidaUso           = 2
	SaidaNaoEncontrado = 3
	SaidaIndisponivel  = 4
	SaidaQuorum        = 5
	SaidaConflito      = 6
	SaidaAutenticacao  = 7
)

type ErroSaida struct {
	Codigo int
	Msg    string
}

func (e *ErroSaida) Error() string { return e.Msg }

func CodigoSaida(err error) int {
	if err == nil {
		return SaidaOK
	}
	var es *ErroSaida
	if errors.As(err, &es) {
		return es.Codigo
	}
	var eu *ErroUso
	if errors.As(err, &eu) {
		return SaidaUso
	}
	var pe *protocolo.Erro
	if errors.As(err, &pe) {
		switch pe.Codigo {
		case protocolo.CodigoNaoEncontrado:
			return SaidaNaoEncontrado
		case protocolo.CodigoIndisponivel, protocolo.CodigoTempoEsgotado:
			return SaidaIndisponivel
		case protocolo.CodigoQuorum:
			return SaidaQuorum
		case protocolo.CodigoConflito:
			return SaidaConflito
		case protocolo.CodigoNaoAutorizado:
			return SaidaAutenticacao
		case protocolo.CodigoRequisicaoInvalida:
			return SaidaUso
		}
	}
	return SaidaErro
}

func Buscar(tokens []string) (*Comando, []string) {
	if len(tokens) == 0 {
		return nil, nil
	}
	primeira := strings.ToLower(tokens[0])
	for _, g := range Grupos {
		if primeira == g.Nome || contem(g.Apelidos, primeira) {
			if len(tokens) < 2 {
				return nil, tokens
			}
			segunda := strings.ToLower(tokens[1])
			for _, c := range Registro {
				if c.Pai == g.Nome && (c.Nome == segunda || contem(c.Apelidos, segunda)) {
					return c, tokens[2:]
				}
			}
			return nil, tokens
		}
	}
	for _, c := range Registro {
		if c.Pai == "" && (c.Nome == primeira || contem(c.Apelidos, primeira)) {
			return c, tokens[1:]
		}
	}
	return nil, tokens
}

func contem(lista []string, v string) bool {
	for _, x := range lista {
		if x == v {
			return true
		}
	}
	return false
}

func Analisar(c *Comando, tokens []string) (Args, error) {
	a := Args{Flags: map[string]string{}}
	porNome := map[string]Flag{}
	porCurta := map[string]Flag{}
	for _, f := range c.Flags {
		porNome[f.Nome] = f
		if f.Curta != "" {
			porCurta[f.Curta] = f
		}
	}
	soPos := false
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if soPos || t == "-" || !strings.HasPrefix(t, "-") {
			a.Pos = append(a.Pos, t)
			continue
		}
		if t == "--" {
			soPos = true
			continue
		}
		var f Flag
		var ok bool
		valor, temValor := "", false
		if strings.HasPrefix(t, "--") {
			nome := t[2:]
			if k := strings.IndexByte(nome, '='); k >= 0 {
				nome, valor, temValor = nome[:k], nome[k+1:], true
			}
			f, ok = porNome[nome]
		} else {
			f, ok = porCurta[t[1:]]
		}
		if !ok {
			if _, err := strconv.ParseFloat(t, 64); err == nil {
				a.Pos = append(a.Pos, t)
				continue
			}
			return a, Uso("opção desconhecida %s para %s", t, c.Caminho())
		}
		if f.Tipo == FlagBooleano {
			if temValor {
				a.Flags[f.Nome] = valor
			} else {
				a.Flags[f.Nome] = "true"
			}
			continue
		}
		if !temValor {
			if i+1 >= len(tokens) {
				return a, Uso("a opção --%s precisa de um valor", f.Nome)
			}
			i++
			valor = tokens[i]
		}
		a.Flags[f.Nome] = valor
	}
	return a, nil
}

func Validar(c *Comando, a Args) error {
	if len(a.Pos) < c.Min {
		return Uso("uso: %s", c.Assinatura())
	}
	if c.Max >= 0 && len(a.Pos) > c.Max {
		return Uso("argumentos demais. Uso: %s", c.Assinatura())
	}
	return nil
}

func Executar(ctx context.Context, s *Sessao, c *Comando, a Args) (*saida.Resultado, error) {
	if err := Validar(c, a); err != nil {
		return nil, err
	}
	inicio := time.Now()
	r, err := c.Exec(ctx, s, a)
	if r != nil {
		r.Duracao = time.Since(inicio)
	}
	return r, err
}

func Nomes() []string {
	var out []string
	for _, c := range Registro {
		out = append(out, c.Caminho())
	}
	sort.Strings(out)
	return out
}

func PorGrupo() map[string][]*Comando {
	out := map[string][]*Comando{}
	for _, c := range Registro {
		out[c.Grupo] = append(out[c.Grupo], c)
	}
	return out
}

func Ajuda(c *Comando, e saida.Estilo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n", e.Negrito(c.Assinatura()), c.Resumo)
	if c.Descricao != "" {
		fmt.Fprintf(&b, "\n%s\n", c.Descricao)
	}
	if len(c.Apelidos) > 0 {
		fmt.Fprintf(&b, "\n%s %s\n", e.Fraco("apelidos:"), strings.Join(c.Apelidos, ", "))
	}
	if len(c.Flags) > 0 {
		b.WriteString("\n" + e.Fraco("opções:") + "\n")
		for _, f := range c.Flags {
			nome := "--" + f.Nome
			if f.Curta != "" {
				nome = "-" + f.Curta + ", " + nome
			}
			fmt.Fprintf(&b, "  %-24s %s\n", nome, f.Ajuda)
		}
	}
	if len(c.Exemplos) > 0 {
		b.WriteString("\n" + e.Fraco("exemplos:") + "\n")
		for _, x := range c.Exemplos {
			fmt.Fprintf(&b, "  %s\n", x)
		}
	}
	return b.String()
}
