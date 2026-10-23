package cli

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/armazenamento"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/cliente"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/comandos"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/config"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/console"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/no"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/protocolo"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/saida"
)

func sinalizar(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
}

func (a *app) cmdConsole() *cobra.Command {
	var endereco, tokenConsole string
	cmd := &cobra.Command{
		Use:     "console",
		Aliases: []string{"ui", "web"},
		Short:   "Abre o console web do cluster no navegador",
		Long:    "Sobe um servidor HTTP local com o console web: visão geral, nós, anel, navegador de chaves e um terminal com os mesmos comandos da CLI.\nPor segurança escuta só em 127.0.0.1; para expor na rede, defina --token-console.",
		Example: "  leme console\n  leme console --endereco 0.0.0.0:8080 --token-console \"$(openssl rand -hex 16)\"",
		GroupID: "Ferramentas",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.sessao(cmd.Context())
			if err != nil {
				return err
			}
			srv, err := console.Novo(console.Config{Cliente: s.Cliente, Consistencia: s.Consistencia, Endereco: endereco, Token: tokenConsole})
			if err != nil {
				return comandos.Uso("%v", err)
			}
			if err := srv.Iniciar(); err != nil {
				return err
			}
			e := a.estilo()
			fmt.Fprintf(a.saida, "%s console em %s\n", e.Ponto("ok"), e.Negrito(srv.URL()))
			fmt.Fprintln(a.saida, e.Fraco("Ctrl+C encerra"))
			ctx, cancelar := sinalizar(cmd.Context())
			defer cancelar()
			<-ctx.Done()
			c, fim := context.WithTimeout(context.Background(), 3*time.Second)
			defer fim()
			return srv.Parar(c)
		},
	}
	cmd.Flags().StringVar(&endereco, "endereco", "127.0.0.1:8080", "endereço HTTP do console")
	cmd.Flags().StringVar(&tokenConsole, "token-console", os.Getenv("LEME_TOKEN_CONSOLE"), "exige este token para abrir o console (LEME_TOKEN_CONSOLE)")
	return cmd
}

func (a *app) cmdDev() *cobra.Command {
	var quantidade, porta, replicas int
	var dados, consoleEnd string
	var logs, limpar bool
	cmd := &cobra.Command{
		Use:     "dev",
		Aliases: []string{"local"},
		Short:   "Sobe um cluster local de teste neste processo",
		Long:    "Inicia N nós do cluster na sua máquina, já ligados entre si, e opcionalmente o console web. É a forma mais rápida de experimentar a CLI.\nOs dados ficam em --dados e sobrevivem a reinícios; use --limpar para começar do zero.",
		Example: "  leme dev\n  leme dev --quantidade 5 --replicas 3 --console 127.0.0.1:8080\n  leme dev --limpar --logs",
		GroupID: "Ferramentas",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if quantidade < 1 || quantidade > 32 {
				return comandos.Uso("--quantidade deve ficar entre 1 e 32")
			}
			if limpar {
				os.RemoveAll(dados)
			}
			nivel := slog.LevelWarn
			if logs {
				nivel = slog.LevelInfo
			}
			log := slog.New(slog.NewTextHandler(a.erro, &slog.HandlerOptions{Level: nivel}))
			e := a.estilo()
			var nos []*no.No
			var enderecos []string
			for i := 0; i < quantidade; i++ {
				end := fmt.Sprintf("127.0.0.1:%d", porta+i)
				n, err := no.Novo(no.Config{ID: fmt.Sprintf("no-%d", i+1), Cluster: "dev", Endereco: end, Dados: dados, Sementes: append([]string{}, enderecos...), Replicas: replicas, Token: a.token, Log: log.With("no", fmt.Sprintf("no-%d", i+1)), Armazenamento: armazenamento.Opcoes{SegmentoMax: 8 << 20}})
				if err != nil {
					return err
				}
				if err := n.Iniciar(cmd.Context()); err != nil {
					for _, x := range nos {
						x.Parar(context.Background())
					}
					return err
				}
				nos = append(nos, n)
				enderecos = append(enderecos, n.Endereco())
			}
			parar := func() {
				c, fim := context.WithTimeout(context.Background(), 5*time.Second)
				defer fim()
				var wg sync.WaitGroup
				for _, n := range nos {
					wg.Add(1)
					go func(n *no.No) { defer wg.Done(); n.Parar(c) }(n)
				}
				wg.Wait()
			}
			defer parar()
			fmt.Fprintf(a.saida, "%s cluster local com %d nós · %d réplicas · dados em %s\n", e.Ponto("ok"), quantidade, replicas, dados)
			for _, n := range nos {
				fmt.Fprintf(a.saida, "  %s  %s\n", n.ID(), n.Endereco())
			}
			if consoleEnd != "" {
				cli, err := cliente.Novo(cliente.Config{Nos: enderecos, Token: a.token})
				if err != nil {
					return err
				}
				defer cli.Fechar()
				time.Sleep(150 * time.Millisecond)
				srv, err := console.Novo(console.Config{Cliente: cli, Endereco: consoleEnd})
				if err != nil {
					return err
				}
				if err := srv.Iniciar(); err != nil {
					return err
				}
				defer srv.Parar(context.Background())
				fmt.Fprintf(a.saida, "%s console em %s\n", e.Ponto("ok"), e.Negrito(srv.URL()))
			}
			fmt.Fprintf(a.saida, "\nEm outro terminal: %s\n", e.Negrito("leme --nos "+strings.Join(enderecos, ",")))
			fmt.Fprintln(a.saida, e.Fraco("Ctrl+C encerra e guarda os dados"))
			ctx, cancelar := sinalizar(cmd.Context())
			defer cancelar()
			<-ctx.Done()
			fmt.Fprintln(a.saida, "\nencerrando…")
			return nil
		},
	}
	cmd.Flags().IntVar(&quantidade, "quantidade", 3, "quantidade de nós")
	cmd.Flags().IntVar(&porta, "porta", 7401, "porta do primeiro nó (os outros usam as seguintes)")
	cmd.Flags().IntVar(&replicas, "replicas", 3, "cópias de cada chave")
	cmd.Flags().StringVar(&dados, "dados", filepath.Join(config.DiretorioDados(), "dev"), "pasta dos dados")
	cmd.Flags().StringVar(&consoleEnd, "console", "", "também sobe o console web neste endereço (ex.: 127.0.0.1:8080)")
	cmd.Flags().BoolVar(&logs, "logs", false, "mostra os logs dos nós")
	cmd.Flags().BoolVar(&limpar, "limpar", false, "apaga os dados antes de subir")
	return cmd
}

func (a *app) cmdBench() *cobra.Command {
	var operacoes, concorrencia, tamanho, chaves int
	var leituras float64
	var prefixo string
	cmd := &cobra.Command{
		Use:     "bench",
		Aliases: []string{"carga"},
		Short:   "Mede vazão e latência com uma carga sintética",
		Example: "  leme bench\n  leme bench --operacoes 50000 --concorrencia 64 --leituras 0.9 --tamanho 512",
		GroupID: "Ferramentas",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if leituras < 0 || leituras > 1 {
				return comandos.Uso("--leituras deve ficar entre 0 e 1")
			}
			if concorrencia < 1 || operacoes < 1 || chaves < 1 {
				return comandos.Uso("--operacoes, --concorrencia e --chaves devem ser maiores que zero")
			}
			s, err := a.sessao(cmd.Context())
			if err != nil {
				return err
			}
			ctx, cancelar := sinalizar(cmd.Context())
			defer cancelar()
			valor := make([]byte, tamanho)
			for i := range valor {
				valor[i] = byte('a' + i%26)
			}
			e := a.estilo()
			fmt.Fprintf(a.saida, "aquecendo %d chaves…\n", chaves)
			var prox atomic.Int64
			var wg sync.WaitGroup
			for w := 0; w < concorrencia; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						i := prox.Add(1) - 1
						if i >= int64(chaves) || ctx.Err() != nil {
							return
						}
						s.Cliente.Put(ctx, []byte(fmt.Sprintf("%s%08d", prefixo, i)), valor, cliente.OpcoesPut{Consistencia: s.Consistencia})
					}
				}()
			}
			wg.Wait()
			lat := make([]time.Duration, operacoes)
			tipo := make([]bool, operacoes)
			var erros atomic.Int64
			prox.Store(0)
			inicio := time.Now()
			for w := 0; w < concorrencia; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						i := prox.Add(1) - 1
						if i >= int64(operacoes) || ctx.Err() != nil {
							return
						}
						k := []byte(fmt.Sprintf("%s%08d", prefixo, rand.IntN(chaves)))
						t0 := time.Now()
						var err error
						if rand.Float64() < leituras {
							_, err = s.Cliente.Get(ctx, k, s.Consistencia)
							tipo[i] = true
						} else {
							_, err = s.Cliente.Put(ctx, k, valor, cliente.OpcoesPut{Consistencia: s.Consistencia})
						}
						lat[i] = time.Since(t0)
						if err != nil && protocolo.CodigoDe(err) != protocolo.CodigoNaoEncontrado {
							erros.Add(1)
						}
					}
				}()
			}
			wg.Wait()
			total := time.Since(inicio)
			feitas := int(prox.Load())
			if feitas > operacoes {
				feitas = operacoes
			}
			var lg, lp []time.Duration
			for i := 0; i < feitas; i++ {
				if lat[i] == 0 {
					continue
				}
				if tipo[i] {
					lg = append(lg, lat[i])
				} else {
					lp = append(lp, lat[i])
				}
			}
			g := &saida.Grade{Colunas: []string{"OPERAÇÃO", "QUANTIDADE", "P50", "P95", "P99", "MÁXIMA"}, Direita: []int{1, 2, 3, 4, 5}}
			dados := map[string]any{"operacoes": feitas, "segundos": total.Seconds(), "ops_por_segundo": float64(feitas) / total.Seconds(), "erros": erros.Load(), "concorrencia": concorrencia, "tamanho_valor": tamanho}
			for _, par := range []struct {
				nome string
				l    []time.Duration
			}{{"get", lg}, {"put", lp}} {
				if len(par.l) == 0 {
					continue
				}
				sort.Slice(par.l, func(i, j int) bool { return par.l[i] < par.l[j] })
				p := func(q float64) time.Duration { return par.l[int(q*float64(len(par.l)-1))] }
				g.Linhas = append(g.Linhas, []string{par.nome, fmt.Sprint(len(par.l)), saida.Duracao(p(0.5)), saida.Duracao(p(0.95)), saida.Duracao(p(0.99)), saida.Duracao(par.l[len(par.l)-1])})
				dados[par.nome] = map[string]any{"quantidade": len(par.l), "p50_ms": ms(p(0.5)), "p95_ms": ms(p(0.95)), "p99_ms": ms(p(0.99)), "max_ms": ms(par.l[len(par.l)-1])}
			}
			res := &saida.Resultado{Grade: g, Dados: dados, Rodape: fmt.Sprintf("%d operações em %s · %.0f ops/s · %d erros · %d clientes simultâneos · valores de %s", feitas, saida.Duracao(total), float64(feitas)/total.Seconds(), erros.Load(), concorrencia, saida.Bytes(int64(tamanho)))}
			f, err := a.formatoSaida()
			if err != nil {
				return err
			}
			return saida.Escrever(a.saida, res, f, e)
		},
	}
	cmd.Flags().IntVar(&operacoes, "operacoes", 10000, "total de operações medidas")
	cmd.Flags().IntVar(&concorrencia, "concorrencia", 32, "clientes simultâneos")
	cmd.Flags().IntVar(&tamanho, "tamanho", 128, "tamanho do valor em bytes")
	cmd.Flags().IntVar(&chaves, "chaves", 1000, "quantidade de chaves usadas")
	cmd.Flags().Float64Var(&leituras, "leituras", 0.8, "fração de leituras (0 a 1)")
	cmd.Flags().StringVar(&prefixo, "prefixo", "bench:", "prefixo das chaves de teste")
	return cmd
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func (a *app) cmdConfig() *cobra.Command {
	raiz := &cobra.Command{Use: "config", Aliases: []string{"perfil"}, Short: "Gerencia perfis de conexão (nós, token, TLS e padrões)", GroupID: "Ferramentas", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() }}
	carregar := func() (*config.Arquivo, error) { return config.Carregar(a.configArq) }

	lista := &cobra.Command{Use: "list", Aliases: []string{"listar", "ls"}, Short: "Lista os perfis", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		arq, err := carregar()
		if err != nil {
			return err
		}
		e := a.estilo()
		g := &saida.Grade{Colunas: []string{"", "PERFIL", "NÓS", "TOKEN", "TLS", "CONSISTÊNCIA"}}
		var dados []map[string]any
		for _, n := range arq.Nomes() {
			p := arq.Perfis[n]
			atual := " "
			if n == arq.Atual {
				atual = e.Verde("*")
			}
			tok := "—"
			if p.Token != "" || p.TokenArquivo != "" {
				tok = "definido"
			}
			tls := "—"
			if p.TLSCA != "" || p.TLSInseguro || p.TLSCert != "" {
				tls = "sim"
			}
			g.Linhas = append(g.Linhas, []string{atual, n, strings.Join(p.Nos, ","), tok, tls, primeiro(p.Consistencia, "quorum")})
			dados = append(dados, map[string]any{"nome": n, "atual": n == arq.Atual, "nos": p.Nos, "consistencia": p.Consistencia})
		}
		f, err := a.formatoSaida()
		if err != nil {
			return err
		}
		return saida.Escrever(a.saida, &saida.Resultado{Grade: g, Dados: dados, Rodape: abreviar(arq.Caminho())}, f, e)
	}}

	usar := &cobra.Command{Use: "use <perfil>", Aliases: []string{"usar"}, Short: "Define o perfil padrão", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		arq, err := carregar()
		if err != nil {
			return err
		}
		if _, ok := arq.Perfis[args[0]]; !ok {
			return comandos.Uso("perfil %q não existe", args[0])
		}
		arq.Atual = args[0]
		if err := arq.Salvar(); err != nil {
			return err
		}
		fmt.Fprintf(a.saida, "perfil padrão: %s\n", args[0])
		return nil
	}}

	var p config.Perfil
	var nosPerfil []string
	definir := &cobra.Command{Use: "set <perfil>", Aliases: []string{"definir", "criar"}, Short: "Cria ou altera um perfil", Args: cobra.ExactArgs(1),
		Example: "  leme config set producao --nos 10.0.0.11:7400,10.0.0.12:7400 --token-arquivo ~/.leme-token --tls-ca ca.pem\n  leme config set local --nos 127.0.0.1:7401 --consistencia um",
		RunE: func(cmd *cobra.Command, args []string) error {
			arq, err := carregar()
			if err != nil {
				return err
			}
			atual := arq.Perfis[args[0]]
			fl := cmd.Flags()
			if fl.Changed("nos") {
				atual.Nos = nosPerfil
			}
			if fl.Changed("token") {
				atual.Token = a.token
			}
			if fl.Changed("token-arquivo") {
				atual.TokenArquivo = a.tokenArquivo
			}
			if fl.Changed("tls-ca") {
				atual.TLSCA = a.tlsCA
			}
			if fl.Changed("tls-cert") {
				atual.TLSCert = a.tlsCert
			}
			if fl.Changed("tls-chave") {
				atual.TLSChave = a.tlsChave
			}
			if fl.Changed("tls-inseguro") {
				atual.TLSInseguro = a.tlsInseguro
			}
			if fl.Changed("consistencia") {
				if _, err := protocolo.ParseConsistencia(p.Consistencia); err != nil {
					return comandos.Uso("%v", err)
				}
				atual.Consistencia = p.Consistencia
			}
			if fl.Changed("tempo-limite") {
				atual.TempoLimite = a.tempoLimite.String()
			}
			if fl.Changed("saida") {
				if _, err := saida.ParseFormato(a.formato); err != nil {
					return comandos.Uso("%v", err)
				}
				atual.Saida = a.formato
			}
			if len(atual.Nos) == 0 {
				return comandos.Uso("informe --nos para o perfil")
			}
			arq.Perfis[args[0]] = atual
			if arq.Atual == "" {
				arq.Atual = args[0]
			}
			if err := arq.Salvar(); err != nil {
				return err
			}
			fmt.Fprintf(a.saida, "perfil %s salvo em %s\n", args[0], abreviar(arq.Caminho()))
			return nil
		}}
	definir.Flags().StringSliceVar(&nosPerfil, "nos", nil, "nós do cluster")
	definir.Flags().StringVar(&p.Consistencia, "consistencia", "", "consistência padrão: um, quorum ou todos")

	apagar := &cobra.Command{Use: "delete <perfil>", Aliases: []string{"apagar", "rm"}, Short: "Apaga um perfil", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		arq, err := carregar()
		if err != nil {
			return err
		}
		if _, ok := arq.Perfis[args[0]]; !ok {
			return comandos.Uso("perfil %q não existe", args[0])
		}
		delete(arq.Perfis, args[0])
		if arq.Atual == args[0] {
			arq.Atual = ""
		}
		if err := arq.Salvar(); err != nil {
			return err
		}
		fmt.Fprintf(a.saida, "perfil %s apagado\n", args[0])
		return nil
	}}

	ver := &cobra.Command{Use: "view", Aliases: []string{"ver", "mostrar"}, Short: "Mostra a conexão que será usada (sem o token)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := a.configCliente()
		if err != nil {
			return err
		}
		tok := "—"
		if cfg.Token != "" {
			tok = "definido (" + fmt.Sprint(len(cfg.Token)) + " caracteres)"
		}
		tls := "desligado"
		if cfg.TLS != nil {
			tls = "ligado"
		}
		lim := cfg.TempoLimite
		if lim == 0 {
			lim = 5 * time.Second
		}
		cons := cfg.Consistencia.String()
		if cfg.Consistencia == protocolo.ConsistenciaPadrao {
			cons = "quorum"
		}
		res := &saida.Resultado{Pares: []saida.Par{{Rotulo: "nós", Valor: strings.Join(cfg.Nos, ", ")}, {Rotulo: "token", Valor: tok}, {Rotulo: "tls", Valor: tls}, {Rotulo: "consistência", Valor: cons}, {Rotulo: "tempo-limite", Valor: lim.String()}, {Rotulo: "arquivo", Valor: abreviar(config.CaminhoPadrao())}},
			Dados: map[string]any{"nos": cfg.Nos, "token": cfg.Token != "", "tls": cfg.TLS != nil, "consistencia": cons, "tempo_limite": lim.String()}}
		f, err := a.formatoSaida()
		if err != nil {
			return err
		}
		return saida.Escrever(a.saida, res, f, a.estilo())
	}}
	raiz.AddCommand(lista, usar, definir, apagar, ver)
	return raiz
}
