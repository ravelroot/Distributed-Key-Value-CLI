package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/cliente"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/comandos"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/no"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/saida"
)

func (a *app) cmdExport() *cobra.Command {
	var arquivo, formato string
	cmd := &cobra.Command{
		Use:     "export [prefixo]",
		Aliases: []string{"exportar"},
		Short:   "Exporta chaves para NDJSON, JSON ou CSV (backup lógico)",
		Long: "Percorre todas as chaves do prefixo e grava chave, valor, TTL restante e versão.\n" +
			"Valores binários vão em base64. Sem --arquivo escreve na saída padrão; o resumo vai para a saída de erro.",
		Example: "  leme export > backup.ndjson\n  leme export usuario: --arquivo usuarios.csv\n  leme export pedido: --formato json | jq length",
		GroupID: "Dados",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prefixo := ""
			if len(args) == 1 {
				prefixo = args[0]
			}
			if formato == "" {
				formato = comandos.FormatoPorNome(arquivo)
			}
			s, err := a.sessao(cmd.Context())
			if err != nil {
				return err
			}
			ctx, cancelar := sinalizar(cmd.Context())
			defer cancelar()
			var w io.Writer = a.saida
			if arquivo != "" && arquivo != "-" {
				f, err := os.OpenFile(arquivo, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
				if err != nil {
					return err
				}
				defer f.Close()
				w = f
			}
			inicio := time.Now()
			n, err := comandos.Exportar(ctx, s, prefixo, formato, w)
			if err != nil {
				return err
			}
			destino := "a saída padrão"
			if arquivo != "" && arquivo != "-" {
				destino = arquivo
			}
			e := a.estilo()
			fmt.Fprintf(a.erro, "%s %d chaves exportadas para %s em %s\n", e.Verde("✓"), n, destino, saida.Duracao(time.Since(inicio)))
			return nil
		},
	}
	cmd.Flags().StringVarP(&arquivo, "arquivo", "a", "", "arquivo de destino (padrão: saída padrão)")
	cmd.Flags().StringVar(&formato, "formato", "", "ndjson (padrão), json ou csv; deduzido pela extensão do arquivo")
	return cmd
}

func (a *app) cmdImport() *cobra.Command {
	var formato, prefixo string
	var seAusente, simular bool
	cmd := &cobra.Command{
		Use:     "import <arquivo | ->",
		Aliases: []string{"importar"},
		Short:   "Importa chaves de um arquivo exportado (NDJSON, JSON ou CSV)",
		Long: "Grava cada chave do arquivo preservando o TTL restante, com até 16 escritas em paralelo.\n" +
			"O formato vem da extensão ou do conteúdo. --simular confere o arquivo sem gravar nada.",
		Example: "  leme import backup.ndjson\n  leme import usuarios.csv --se-ausente\n  leme import backup.ndjson --prefixo homolog: --simular\n  leme export | leme --perfil destino import -",
		GroupID: "Dados",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var r io.Reader = a.entrada
			if args[0] != "-" {
				f, err := os.Open(args[0])
				if err != nil {
					return err
				}
				defer f.Close()
				r = f
				if formato == "" {
					formato = comandos.FormatoPorNome(args[0])
				}
			}
			s, err := a.sessao(cmd.Context())
			if err != nil {
				return err
			}
			ctx, cancelar := sinalizar(cmd.Context())
			defer cancelar()
			inicio := time.Now()
			rel, err := comandos.Importar(ctx, s, r, comandos.OpcoesImportar{Formato: formato, SeAusente: seAusente, Simular: simular, Prefixo: prefixo})
			f, ferr := a.formatoSaida()
			if ferr != nil {
				return ferr
			}
			e := a.estilo()
			res := &saida.Resultado{Dados: rel, Pares: []saida.Par{
				{Rotulo: "lidas", Valor: strconv.Itoa(rel.Lidos)},
				{Rotulo: "gravadas", Valor: strconv.Itoa(rel.Gravados)},
				{Rotulo: "ignoradas", Valor: strconv.Itoa(rel.Ignorados)},
				{Rotulo: "falhas", Valor: strconv.Itoa(rel.Falhas)},
			}}
			if simular {
				res.Pares[1].Rotulo = "seriam gravadas"
				res.Titulo = "Simulação (nada foi gravado)"
			}
			if len(rel.Erros) > 0 {
				res.Rodape = "primeiros erros:\n  " + strings.Join(rel.Erros, "\n  ")
			} else {
				res.Rodape = "em " + saida.Duracao(time.Since(inicio))
			}
			saida.Escrever(a.saida, res, f, e)
			if err != nil {
				return err
			}
			if rel.Falhas > 0 {
				return &comandos.ErroSaida{Codigo: comandos.SaidaErro, Msg: fmt.Sprintf("%d chaves não foram importadas", rel.Falhas)}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&formato, "formato", "", "ndjson, json ou csv (padrão: pela extensão ou pelo conteúdo)")
	cmd.Flags().StringVar(&prefixo, "prefixo", "", "acrescenta este prefixo ao nome de cada chave")
	cmd.Flags().BoolVarP(&seAusente, "se-ausente", "n", false, "não sobrescreve chaves que já existem")
	cmd.Flags().BoolVar(&simular, "simular", false, "só confere o arquivo e conta, sem gravar")
	return cmd
}

type estadoVigia struct {
	versao uint64
	expira int64
	valor  []byte
}

func (a *app) cmdWatch() *cobra.Command {
	var prefixo string
	var intervalo time.Duration
	var vezes int
	cmd := &cobra.Command{
		Use:     "watch [chave]",
		Aliases: []string{"vigiar"},
		Short:   "Acompanha as mudanças de uma chave ou de um prefixo até Ctrl+C",
		Long: "Consulta o cluster a cada intervalo e mostra cada mudança: criada, alterada, apagada ou expirada.\n" +
			"Com -o json cada evento sai como uma linha JSON, pronto para outro programa consumir.",
		Example: "  leme watch config:tema\n  leme watch --prefixo pedido: --intervalo 500ms\n  leme watch --prefixo sessao: -o json | jq -c .",
		GroupID: "Ferramentas",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (len(args) == 1) == (prefixo != "") {
				return comandos.Uso("informe uma chave ou --prefixo, um dos dois")
			}
			if intervalo < 100*time.Millisecond {
				return comandos.Uso("--intervalo mínimo é 100ms")
			}
			s, err := a.sessao(cmd.Context())
			if err != nil {
				return err
			}
			ctx, cancelar := sinalizar(cmd.Context())
			defer cancelar()
			f, err := a.formatoSaida()
			if err != nil {
				return err
			}
			e := a.estilo()
			alvo := prefixo + "*"
			if len(args) == 1 {
				alvo = args[0]
			}
			if f != saida.JSON {
				fmt.Fprintf(a.erro, "%s vigiando %s a cada %s · Ctrl+C para sair\n", e.Fraco("›"), alvo, intervalo)
			}
			anterior := map[string]estadoVigia{}
			primeira := true
			for rodada := 0; vezes == 0 || rodada < vezes; rodada++ {
				atual, err := a.coletarVigia(ctx, s, args, prefixo)
				if err != nil {
					if ctx.Err() != nil {
						return nil
					}
					fmt.Fprintf(a.erro, "%s %v\n", e.Amarelo("!"), err)
				} else {
					if !primeira {
						a.emitirMudancas(anterior, atual, f, e)
					} else if f != saida.JSON {
						fmt.Fprintf(a.erro, "%s %d chaves no estado inicial\n", e.Fraco("›"), len(atual))
					}
					anterior, primeira = atual, false
				}
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(intervalo):
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&prefixo, "prefixo", "p", "", "vigia todas as chaves com este prefixo")
	cmd.Flags().DurationVarP(&intervalo, "intervalo", "i", time.Second, "tempo entre consultas")
	cmd.Flags().IntVar(&vezes, "vezes", 0, "para depois de N consultas (0 = até Ctrl+C)")
	return cmd
}

func (a *app) coletarVigia(ctx context.Context, s *comandos.Sessao, args []string, prefixo string) (map[string]estadoVigia, error) {
	m := map[string]estadoVigia{}
	if len(args) == 1 {
		v, err := s.Cliente.Get(ctx, []byte(args[0]), s.Consistencia)
		if comandos.CodigoSaida(err) == comandos.SaidaNaoEncontrado {
			return m, nil
		}
		if err != nil {
			return nil, err
		}
		m[args[0]] = estadoVigia{versao: v.Versao, expira: v.Expira, valor: v.Valor}
		return m, nil
	}
	depois := ""
	for {
		pag, mais, err := s.Cliente.Scan(ctx, cliente.OpcoesScan{Prefixo: prefixo, Depois: depois, Limite: 1000})
		if err != nil {
			return nil, err
		}
		for _, it := range pag {
			m[string(it.Chave)] = estadoVigia{versao: it.Versao, expira: it.Expira, valor: it.Valor}
		}
		if !mais || len(pag) == 0 || len(m) > 100000 {
			return m, nil
		}
		depois = string(pag[len(pag)-1].Chave)
	}
}

func (a *app) emitirMudancas(antes, depois map[string]estadoVigia, f saida.Formato, e saida.Estilo) {
	type evento struct {
		Hora   string `json:"hora"`
		Evento string `json:"evento"`
		Chave  string `json:"chave"`
		Versao string `json:"versao,omitempty"`
		Valor  string `json:"valor,omitempty"`
	}
	var evs []evento
	hora := time.Now().Format("15:04:05.000")
	for k, d := range depois {
		ant, existia := antes[k]
		switch {
		case !existia:
			evs = append(evs, evento{hora, "criada", k, strconv.FormatUint(d.versao, 10), saida.Exibivel(d.valor)})
		case ant.versao != d.versao:
			evs = append(evs, evento{hora, "alterada", k, strconv.FormatUint(d.versao, 10), saida.Exibivel(d.valor)})
		}
	}
	agora := time.Now().UnixMilli()
	for k, ant := range antes {
		if _, ok := depois[k]; !ok {
			tipo := "apagada"
			if ant.expira > 0 && ant.expira <= agora+1000 {
				tipo = "expirada"
			}
			evs = append(evs, evento{Hora: hora, Evento: tipo, Chave: k})
		}
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].Chave < evs[j].Chave })
	for _, ev := range evs {
		if f == saida.JSON {
			b, _ := json.Marshal(ev)
			fmt.Fprintln(a.saida, string(b))
			continue
		}
		rotulo := fmt.Sprintf("%-8s", ev.Evento)
		switch ev.Evento {
		case "criada":
			rotulo = e.Verde(rotulo)
		case "alterada":
			rotulo = e.Amarelo(rotulo)
		default:
			rotulo = e.Vermelho(rotulo)
		}
		linha := fmt.Sprintf("%s  %s  %s", e.Fraco(ev.Hora), rotulo, ev.Chave)
		if ev.Valor != "" {
			linha += "  " + e.Fraco(saida.Resumir([]byte(ev.Valor), 60))
		}
		fmt.Fprintln(a.saida, linha)
	}
}

func opsUsuario(st no.Estatisticas) uint64 {
	var t uint64
	for _, op := range st.Operacoes {
		if strings.HasPrefix(op.Op, "REPLICA") || op.Op == "GOSSIP" || op.Op == "SCAN_LOCAL" || op.Op == "ENTRAR" {
			continue
		}
		t += op.Total
	}
	return t
}

func p99Op(st no.Estatisticas, nome string) float64 {
	for _, op := range st.Operacoes {
		if op.Op == nome {
			return op.P99
		}
	}
	return 0
}

func (a *app) cmdTop() *cobra.Command {
	var intervalo time.Duration
	var vezes int
	cmd := &cobra.Command{
		Use:     "top",
		Aliases: []string{"painel"},
		Short:   "Painel ao vivo do cluster no terminal",
		Long:    "Atualiza a cada intervalo: estado de cada nó, operações por segundo, latência p99, chaves, disco e fragmentação.",
		Example: "  leme top\n  leme top --intervalo 5s\n  leme top --vezes 1 --sem-cor > retrato.txt",
		GroupID: "Ferramentas",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if intervalo < 200*time.Millisecond {
				return comandos.Uso("--intervalo mínimo é 200ms")
			}
			s, err := a.sessao(cmd.Context())
			if err != nil {
				return err
			}
			ctx, cancelar := sinalizar(cmd.Context())
			defer cancelar()
			e := a.estilo()
			term := false
			if f, ok := a.saida.(*os.File); ok {
				term = saida.Terminal(f)
			}
			if term {
				fmt.Fprint(a.saida, "\x1b[?25l")
				defer fmt.Fprint(a.saida, "\x1b[?25h")
			}
			anteriores := map[string]uint64{}
			var quando time.Time
			for rodada := 0; vezes == 0 || rodada < vezes; rodada++ {
				r, err := comandos.ColetarResumo(ctx, s)
				if err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return err
				}
				estados := map[string]string{}
				for _, m := range s.Cliente.Membros() {
					estados[m.ID] = m.Estado
				}
				dt := time.Since(quando).Seconds()
				g := &saida.Grade{Colunas: []string{"", "NÓ", "ESTADO", "CHAVES", "DISCO", "OPS/S", "P99 GET", "P99 PUT", "FRAG.", "ANEL"}, Direita: []int{3, 4, 5, 6, 7, 8}}
				totalOps := 0.0
				for _, st := range r.Nos {
					ops := opsUsuario(st)
					taxa := "—"
					if ant, ok := anteriores[st.ID]; ok && dt > 0 && ops >= ant {
						v := float64(ops-ant) / dt
						totalOps += v
						taxa = strconv.FormatFloat(v, 'f', 0, 64)
					}
					anteriores[st.ID] = ops
					est := estados[st.ID]
					if est == "" {
						est = "vivo"
					}
					g.Linhas = append(g.Linhas, []string{e.Ponto(est), st.ID, est, strconv.Itoa(st.Armazenamento.Chaves), saida.Bytes(st.Armazenamento.BytesDisco), taxa,
						fmt.Sprintf("%.2f ms", p99Op(st, "GET")), fmt.Sprintf("%.2f ms", p99Op(st, "PUT")),
						fmt.Sprintf("%.0f%%", st.Armazenamento.Fragmentacao*100), fmt.Sprintf("%s %4.1f%%", barraAnel(st.Participacao, 10), st.Participacao*100)})
				}
				quando = time.Now()
				if term {
					fmt.Fprint(a.saida, "\x1b[H\x1b[2J")
				}
				saudeTxt := r.Saude
				switch r.Saude {
				case "saudável":
					saudeTxt = e.Verde(r.Saude)
				case "degradado":
					saudeTxt = e.Amarelo(r.Saude)
				default:
					saudeTxt = e.Vermelho(r.Saude)
				}
				fmt.Fprintf(a.saida, "%s · cluster %s · %d/%d nós · %s%s\n", e.Negrito("Leme"), r.Cluster, r.Vivos, r.Membros, saudeTxt, e.Fraco("   "+quando.Format("15:04:05")))
				ops := "—"
				if rodada > 0 {
					ops = strconv.FormatFloat(totalOps, 'f', 0, 64)
				}
				fmt.Fprintf(a.saida, "%s\n\n", e.Fraco(fmt.Sprintf("chaves %d · disco %s · ops/s %s · p99 get %.2f ms · put %.2f ms · dicas %d · %d cópias", r.ChavesLogicas, saida.Bytes(r.BytesDisco), ops, r.P99Get, r.P99Put, r.Dicas, r.Replicas)))
				saida.Escrever(a.saida, &saida.Resultado{Grade: g}, saida.Tabela, e)
				if term {
					fmt.Fprintf(a.saida, "\n%s\n", e.Fraco("Ctrl+C para sair"))
				}
				if vezes != 0 && rodada == vezes-1 {
					break
				}
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(intervalo):
				}
			}
			return nil
		},
	}
	cmd.Flags().DurationVarP(&intervalo, "intervalo", "i", 2*time.Second, "tempo entre atualizações")
	cmd.Flags().IntVar(&vezes, "vezes", 0, "para depois de N atualizações (0 = até Ctrl+C)")
	return cmd
}

func barraAnel(f float64, largura int) string {
	n := int(f*float64(largura) + 0.5)
	if n > largura {
		n = largura
	}
	return strings.Repeat("█", n) + strings.Repeat("░", largura-n)
}
