package repl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/cliente"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/comandos"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/protocolo"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/saida"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/terminal"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/versao"
)

type REPL struct {
	Sessao      *comandos.Sessao
	Formato     saida.Formato
	Tempo       bool
	Editor      *terminal.Editor
	Saida       io.Writer
	Erro        io.Writer
	Historico   string
	PararNoErro bool
	Reconectar  func(nos []string) (*cliente.Cliente, error)
	chaves      []string
	codigo      int
}

var meta = []string{"ajuda", "sair", "limpar", `\?`, `\q`, `\saida`, `\consistencia`, `\tempo`, `\conectar`, `\historico`, `\nos`}

func (r *REPL) Rodar(ctx context.Context) int {
	r.Editor.Completar = r.completar
	interativo := r.Editor.Interativo()
	if interativo {
		r.carregarHistorico()
		r.banner(ctx)
	}
	for {
		linha, err := r.Editor.Ler(r.prompt())
		if errors.Is(err, terminal.ErrInterrompido) {
			fmt.Fprintln(r.Saida, r.Sessao.Estilo.Fraco("(use sair ou Ctrl+D para encerrar)"))
			continue
		}
		if err != nil {
			if interativo {
				r.salvarHistorico()
			}
			return r.codigo
		}
		linha = strings.TrimSpace(linha)
		if linha == "" || strings.HasPrefix(linha, "#") {
			continue
		}
		if interativo {
			r.Editor.Adicionar(linha)
		}
		sair, err := r.ExecutarLinha(ctx, linha)
		if err != nil {
			cod := comandos.CodigoSaida(err)
			if r.codigo == 0 {
				r.codigo = cod
			}
			if !interativo && r.PararNoErro {
				return cod
			}
		}
		if sair {
			if interativo {
				r.salvarHistorico()
			}
			return r.codigo
		}
	}
}

func (r *REPL) prompt() string {
	e := r.Sessao.Estilo
	nome := "leme"
	if m := r.Sessao.Cliente.Membros(); len(m) > 0 {
		vivos := 0
		for _, x := range m {
			if x.Estado == "vivo" {
				vivos++
			}
		}
		nome = fmt.Sprintf("leme %s", e.Fraco(fmt.Sprintf("%d/%d", vivos, len(m))))
	}
	return nome + " " + e.Negrito("›") + " "
}

func (r *REPL) banner(ctx context.Context) {
	e := r.Sessao.Estilo
	fmt.Fprintf(r.Saida, "%s %s\n", e.Negrito("Leme"), e.Fraco(versao.Versao+" · CLI interativa do cluster chave-valor"))
	t, err := r.Sessao.Cliente.Topologia(ctx)
	if err != nil {
		fmt.Fprintf(r.Saida, "%s %v\n", e.Ponto("erro"), err)
	} else {
		vivos := 0
		for _, m := range t.Membros {
			if m.Estado == "vivo" {
				vivos++
			}
			r.chaves = append(r.chaves, m.ID)
		}
		fmt.Fprintf(r.Saida, "%s conectado ao cluster %s · %d de %d nós vivos · %d réplicas · consistência %s\n", e.Ponto("ok"), e.Negrito(t.Cluster), vivos, len(t.Membros), t.Replicas, r.consistencia())
	}
	fmt.Fprintln(r.Saida, e.Fraco("ajuda lista os comandos · Tab completa · ↑ ↓ histórico · sair ou Ctrl+D encerra"))
	fmt.Fprintln(r.Saida)
}

func (r *REPL) consistencia() string {
	if r.Sessao.Consistencia == protocolo.ConsistenciaPadrao {
		return "quorum"
	}
	return r.Sessao.Consistencia.String()
}

func (r *REPL) ExecutarLinha(ctx context.Context, linha string) (bool, error) {
	e := r.Sessao.Estilo
	var tokens []string
	var err error
	if strings.HasPrefix(linha, `\`) {
		tokens = strings.Fields(linha)
	} else {
		tokens, err = comandos.Tokenizar(linha)
	}
	if err != nil {
		fmt.Fprintf(r.Erro, "%s %v\n", e.Vermelho("erro:"), err)
		return false, comandos.Uso("%v", err)
	}
	if len(tokens) == 0 {
		return false, nil
	}
	if sair, tratado, err := r.meta(ctx, tokens); tratado {
		return sair, err
	}
	cmd, resto := comandos.Buscar(tokens)
	if cmd == nil {
		if len(resto) > 0 {
			for _, g := range comandos.Grupos {
				if strings.EqualFold(resto[0], g.Nome) || contem(g.Apelidos, strings.ToLower(resto[0])) {
					r.ajudaGrupo(g.Nome)
					return false, nil
				}
			}
		}
		err := comandos.Uso("comando desconhecido: %s (digite ajuda)", tokens[0])
		fmt.Fprintf(r.Erro, "%s %v\n", e.Vermelho("erro:"), err)
		return false, err
	}
	a, err := comandos.Analisar(cmd, resto)
	if err != nil {
		fmt.Fprintf(r.Erro, "%s %v\n", e.Vermelho("erro:"), err)
		return false, err
	}
	a.Interativo = r.Editor.Interativo()
	if cmd.Perigoso && !a.Bool("confirmar") && r.Editor.Interativo() {
		resp, _ := r.Editor.Ler(e.Amarelo("confirma " + cmd.Caminho() + " " + strings.Join(a.Pos, " ") + "? [s/N] "))
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(resp)), "s") {
			fmt.Fprintln(r.Saida, e.Fraco("cancelado"))
			return false, nil
		}
		a.Flags["confirmar"] = "true"
	}
	c, cancelar := context.WithCancel(ctx)
	sinais := make(chan os.Signal, 1)
	signal.Notify(sinais, os.Interrupt)
	go func() {
		select {
		case <-sinais:
			cancelar()
		case <-c.Done():
		}
	}()
	res, err := comandos.Executar(c, r.Sessao, cmd, a)
	signal.Stop(sinais)
	cancelar()
	if res != nil {
		saida.Escrever(r.Saida, res, r.Formato, e)
		if r.Tempo && r.Formato == saida.Tabela {
			fmt.Fprintln(r.Saida, e.Fraco("("+saida.Duracao(res.Duracao)+")"))
		}
	}
	for _, p := range a.Pos {
		r.lembrar(p)
	}
	if err != nil {
		var es *comandos.ErroSaida
		if !(errors.As(err, &es) && es.Msg == "") {
			msg := err.Error()
			if errors.Is(err, context.Canceled) {
				msg = "cancelado"
			}
			fmt.Fprintf(r.Erro, "%s %s\n", e.Vermelho("erro:"), msg)
		}
		return false, err
	}
	return false, nil
}

func contem(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

func (r *REPL) meta(ctx context.Context, t []string) (bool, bool, error) {
	e := r.Sessao.Estilo
	cmd := strings.ToLower(t[0])
	switch cmd {
	case "sair", "exit", "quit", `\q`:
		return true, true, nil
	case "ajuda", "help", `\?`, "?":
		if len(t) > 1 {
			c, _ := comandos.Buscar(t[1:])
			if c == nil {
				r.ajudaGrupo(strings.ToLower(t[1]))
				return false, true, nil
			}
			fmt.Fprint(r.Saida, comandos.Ajuda(c, e))
			return false, true, nil
		}
		r.ajuda()
		return false, true, nil
	case "limpar", "clear", `\l`:
		fmt.Fprint(r.Saida, "\x1b[H\x1b[2J")
		return false, true, nil
	case `\saida`, `\o`:
		if len(t) < 2 {
			fmt.Fprintf(r.Saida, "saída atual: %s\n", r.Formato)
			return false, true, nil
		}
		f, err := saida.ParseFormato(t[1])
		if err != nil {
			fmt.Fprintf(r.Erro, "%s %v\n", e.Vermelho("erro:"), err)
			return false, true, err
		}
		r.Formato = f
		fmt.Fprintf(r.Saida, "saída: %s\n", f)
		return false, true, nil
	case `\consistencia`, `\consistência`:
		if len(t) < 2 {
			fmt.Fprintf(r.Saida, "consistência atual: %s\n", r.consistencia())
			return false, true, nil
		}
		c, err := protocolo.ParseConsistencia(t[1])
		if err != nil {
			fmt.Fprintf(r.Erro, "%s %v\n", e.Vermelho("erro:"), err)
			return false, true, err
		}
		r.Sessao.Consistencia = c
		fmt.Fprintf(r.Saida, "consistência: %s\n", r.consistencia())
		return false, true, nil
	case `\tempo`, `\timing`:
		r.Tempo = !r.Tempo
		estado := "desligado"
		if r.Tempo {
			estado = "ligado"
		}
		fmt.Fprintf(r.Saida, "tempo de execução: %s\n", estado)
		return false, true, nil
	case `\historico`, `\histórico`:
		n := 20
		if len(t) > 1 {
			if v, err := strconv.Atoi(t[1]); err == nil {
				n = v
			}
		}
		h := r.Editor.Historico
		ini := len(h) - n
		if ini < 0 {
			ini = 0
		}
		for i := ini; i < len(h); i++ {
			fmt.Fprintf(r.Saida, "%s  %s\n", e.Fraco(fmt.Sprintf("%4d", i+1)), h[i])
		}
		return false, true, nil
	case `\conectar`, `\c`:
		if len(t) < 2 || r.Reconectar == nil {
			fmt.Fprintln(r.Erro, `uso: \conectar host:porta[,host:porta...]`)
			return false, true, comandos.Uso("faltou o endereço")
		}
		cli, err := r.Reconectar(t[1:])
		if err != nil {
			fmt.Fprintf(r.Erro, "%s %v\n", e.Vermelho("erro:"), err)
			return false, true, err
		}
		r.Sessao.Cliente.Fechar()
		r.Sessao.Cliente = cli
		r.banner(ctx)
		return false, true, nil
	case `\nos`:
		_, err := r.ExecutarLinha(ctx, "cluster nodes")
		return false, true, err
	}
	return false, false, nil
}

func (r *REPL) ajuda() {
	e := r.Sessao.Estilo
	grupos := comandos.PorGrupo()
	for _, g := range comandos.Ordem {
		fmt.Fprintln(r.Saida, e.Negrito(g))
		for _, c := range grupos[g] {
			fmt.Fprintf(r.Saida, "  %-34s %s\n", c.Assinatura(), e.Fraco(c.Resumo))
		}
		fmt.Fprintln(r.Saida)
	}
	fmt.Fprintln(r.Saida, e.Negrito("Sessão"))
	linhas := [][2]string{
		{`ajuda [comando]`, "ajuda geral ou de um comando"},
		{`\saida tabela|json|texto`, "muda o formato da saída"},
		{`\consistencia um|quorum|todos`, "muda a consistência das próximas operações"},
		{`\tempo`, "liga ou desliga o tempo de cada comando"},
		{`\conectar host:porta`, "conecta a outro cluster"},
		{`\historico [n]`, "mostra os últimos comandos"},
		{`limpar`, "limpa a tela (Ctrl+L)"},
		{`sair`, "encerra (Ctrl+D)"},
	}
	for _, l := range linhas {
		fmt.Fprintf(r.Saida, "  %-34s %s\n", l[0], e.Fraco(l[1]))
	}
	fmt.Fprintln(r.Saida)
	fmt.Fprintln(r.Saida, e.Fraco(`Valores com espaço vão entre aspas: put nota:1 "texto com espaços". Atalhos: Ctrl+A/E início e fim, Ctrl+W apaga palavra, Ctrl+U apaga a linha, Ctrl+C cancela.`))
}

func (r *REPL) ajudaGrupo(nome string) {
	e := r.Sessao.Estilo
	achou := false
	for _, c := range comandos.Registro {
		if c.Pai == nome {
			if !achou {
				fmt.Fprintln(r.Saida, e.Negrito(nome))
				achou = true
			}
			fmt.Fprintf(r.Saida, "  %-34s %s\n", c.Assinatura(), e.Fraco(c.Resumo))
		}
	}
	if !achou {
		fmt.Fprintf(r.Erro, "%s comando desconhecido: %s\n", e.Vermelho("erro:"), nome)
	}
}

func (r *REPL) lembrar(k string) {
	if k == "" || len(k) > 200 || strings.HasPrefix(k, "-") {
		return
	}
	for _, x := range r.chaves {
		if x == k {
			return
		}
	}
	r.chaves = append(r.chaves, k)
	if len(r.chaves) > 500 {
		r.chaves = r.chaves[len(r.chaves)-500:]
	}
}

func (r *REPL) completar(linha []rune, pos int) (int, []string) {
	antes := string(linha[:pos])
	ini := strings.LastIndexAny(antes, " \t") + 1
	palavra := antes[ini:]
	anteriores := strings.Fields(antes[:ini])
	var cand []string
	add := func(s ...string) {
		for _, x := range s {
			if strings.HasPrefix(x, palavra) {
				cand = append(cand, x)
			}
		}
	}
	runaIni := len([]rune(antes[:ini]))
	if len(anteriores) == 0 {
		for _, c := range comandos.Registro {
			if c.Pai == "" {
				add(c.Nome)
			}
		}
		for _, g := range comandos.Grupos {
			add(g.Nome)
		}
		add(meta...)
		return runaIni, unicos(cand)
	}
	primeira := strings.ToLower(anteriores[0])
	if len(anteriores) == 1 {
		for _, g := range comandos.Grupos {
			if primeira == g.Nome || contem(g.Apelidos, primeira) {
				for _, c := range comandos.Registro {
					if c.Pai == g.Nome {
						add(c.Nome)
					}
				}
				return runaIni, unicos(cand)
			}
		}
		switch primeira {
		case `\saida`, `\o`:
			add("tabela", "json", "texto")
			return runaIni, cand
		case `\consistencia`:
			add("um", "quorum", "todos")
			return runaIni, cand
		case "ajuda", "help":
			for _, c := range comandos.Registro {
				if c.Pai == "" {
					add(c.Nome)
				}
			}
			return runaIni, unicos(cand)
		}
	}
	cmd, _ := comandos.Buscar(anteriores)
	if strings.HasPrefix(palavra, "-") && cmd != nil {
		for _, f := range cmd.Flags {
			add("--" + f.Nome)
		}
		return runaIni, cand
	}
	if len(anteriores) > 0 && anteriores[len(anteriores)-1] == "--consistencia" || len(anteriores) > 0 && anteriores[len(anteriores)-1] == "-c" {
		add("um", "quorum", "todos")
		return runaIni, cand
	}
	add(r.chaves...)
	return runaIni, unicos(cand)
}

func unicos(l []string) []string {
	sort.Strings(l)
	out := l[:0]
	for i, x := range l {
		if i == 0 || x != l[i-1] {
			out = append(out, x)
		}
	}
	return out
}

func (r *REPL) carregarHistorico() {
	if r.Historico == "" {
		return
	}
	f, err := os.Open(r.Historico)
	if err != nil {
		return
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		r.Editor.Adicionar(s.Text())
	}
}

func (r *REPL) salvarHistorico() {
	if r.Historico == "" || len(r.Editor.Historico) == 0 {
		return
	}
	os.MkdirAll(filepath.Dir(r.Historico), 0o700)
	conteudo := strings.Join(r.Editor.Historico, "\n") + "\n"
	os.WriteFile(r.Historico, []byte(conteudo), 0o600)
}
