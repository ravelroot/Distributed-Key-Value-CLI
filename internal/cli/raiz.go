package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/cliente"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/comandos"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/config"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/protocolo"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/repl"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/saida"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/terminal"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/transporte"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/versao"
)

type app struct {
	entrada      io.Reader
	arqEntrada   *os.File
	saida        io.Writer
	erro         io.Writer
	nos          []string
	perfil       string
	token        string
	tokenArquivo string
	tlsCA        string
	tlsCert      string
	tlsChave     string
	tlsInseguro  bool
	tempoLimite  time.Duration
	formato      string
	consistencia string
	semCor       bool
	configArq    string
	raiz         *cobra.Command
	cli          *cliente.Cliente
}

func Executar(args []string, entrada *os.File, saida, erro io.Writer) int {
	a := &app{entrada: entrada, arqEntrada: entrada, saida: saida, erro: erro}
	raiz := a.construir()
	raiz.SetArgs(protegerNegativos(args))
	raiz.SetIn(entrada)
	raiz.SetOut(saida)
	raiz.SetErr(erro)
	err := raiz.ExecuteContext(context.Background())
	if a.cli != nil {
		a.cli.Fechar()
	}
	if err != nil {
		var cod *codigoSaida
		if errors.As(err, &cod) {
			return cod.n
		}
		var es *comandos.ErroSaida
		if !(errors.As(err, &es) && es.Msg == "") {
			fmt.Fprintf(erro, "%s %s\n", a.estilo().Vermelho("erro:"), mensagem(err))
		}
		if strings.Contains(err.Error(), "unknown command") || strings.Contains(err.Error(), "unknown flag") || strings.Contains(err.Error(), "accepts") {
			return comandos.SaidaUso
		}
		return comandos.CodigoSaida(err)
	}
	return 0
}

type codigoSaida struct{ n int }

func (c *codigoSaida) Error() string { return "" }

func mensagem(err error) string {
	s := err.Error()
	s = strings.Replace(s, "unknown command", "comando desconhecido", 1)
	s = strings.Replace(s, "unknown flag", "opção desconhecida", 1)
	s = strings.Replace(s, "unknown shorthand flag", "opção curta desconhecida", 1)
	s = strings.Replace(s, "for \"leme\"", "", 1)
	s = strings.Replace(s, "Run 'leme --help' for usage.", "Use leme --help.", 1)
	return strings.TrimSpace(s)
}

func (a *app) arquivoSaida() *os.File {
	if f, ok := a.saida.(*os.File); ok {
		return f
	}
	return nil
}

func (a *app) estilo() saida.Estilo {
	if a.semCor {
		return saida.Estilo{}
	}
	if f := a.arquivoSaida(); f != nil {
		return saida.Estilo{Cor: saida.DetectarCor(f)}
	}
	return saida.Estilo{}
}

const modelo = `{{if .Runnable}}Uso:
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [comando]{{end}}{{if gt (len .Aliases) 0}}

Apelidos:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Exemplos:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Comandos:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Outros comandos:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Opções:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Opções globais:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [comando] --help" para saber mais sobre um comando.{{end}}
`

func (a *app) construir() *cobra.Command {
	raiz := &cobra.Command{
		Use:   "leme",
		Short: "CLI interativa para o cluster chave-valor distribuído",
		Long: "Leme conversa com um cluster chave-valor distribuído pelo protocolo binário dos nós.\n" +
			"Sem argumentos abre o modo interativo; com um comando executa e sai, pronto para scripts.",
		Example: `  leme                                   abre o modo interativo
  leme put usuario:42 '{"nome":"Ana"}'    grava um valor
  leme get usuario:42                     lê o valor
  leme scan pedido: --limite 20           lista chaves por prefixo
  leme cluster status                     resumo do cluster
  leme incr visitas:home                  contador atômico
  leme export > backup.ndjson             backup lógico de todas as chaves
  leme doctor                             diagnostica o cluster
  leme top                                painel ao vivo
  leme dev --quantidade 3                 sobe um cluster local para testes
  leme console                            abre o console web`,
		Version:       versao.Versao,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.rodarShell(cmd.Context(), "", false)
		},
	}
	a.raiz = raiz
	raiz.SetUsageTemplate(modelo)
	raiz.SetVersionTemplate("{{.Version}}\n")
	raiz.CompletionOptions.DisableDefaultCmd = true
	raiz.SetHelpCommand(&cobra.Command{Hidden: true, Use: "no-help", GroupID: "Ferramentas"})
	pf := raiz.PersistentFlags()
	pf.BoolP("help", "h", false, "mostra a ajuda")
	pf.StringSliceVar(&a.nos, "nos", nil, "nós do cluster, host:porta separados por vírgula (LEME_NOS)")
	pf.StringVar(&a.perfil, "perfil", "", "perfil de conexão salvo com leme config (LEME_PERFIL)")
	pf.StringVar(&a.token, "token", "", "token de acesso do cluster (LEME_TOKEN)")
	pf.StringVar(&a.tokenArquivo, "token-arquivo", "", "lê o token de um arquivo")
	pf.StringVar(&a.tlsCA, "tls-ca", "", "CA para validar os nós (ativa TLS)")
	pf.StringVar(&a.tlsCert, "tls-cert", "", "certificado do cliente para mTLS")
	pf.StringVar(&a.tlsChave, "tls-chave", "", "chave privada do certificado do cliente")
	pf.BoolVar(&a.tlsInseguro, "tls-inseguro", false, "usa TLS sem validar o certificado (só para testes)")
	pf.DurationVar(&a.tempoLimite, "tempo-limite", 0, "tempo máximo de cada operação (padrão 5s)")
	pf.StringVarP(&a.formato, "saida", "o", "", "formato da saída: tabela, json ou texto (LEME_SAIDA)")
	pf.BoolVar(&a.semCor, "sem-cor", false, "desliga as cores (também NO_COLOR)")
	pf.StringVar(&a.configArq, "config", "", "arquivo de configuração (padrão "+abreviar(config.CaminhoPadrao())+")")
	raiz.Flags().BoolP("version", "v", false, "mostra a versão")

	raiz.AddGroup(&cobra.Group{ID: "Dados", Title: "Dados:"}, &cobra.Group{ID: "Cluster", Title: "Cluster:"}, &cobra.Group{ID: "Nós", Title: "Nós:"}, &cobra.Group{ID: "Ferramentas", Title: "Ferramentas:"})
	pais := map[string]*cobra.Command{}
	for _, g := range comandos.Grupos {
		gid := "Cluster"
		if g.Nome == "node" {
			gid = "Nós"
		}
		p := &cobra.Command{Use: g.Nome, Aliases: g.Apelidos, Short: g.Resumo, GroupID: gid, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() }}
		pais[g.Nome] = p
		raiz.AddCommand(p)
	}
	for _, c := range comandos.Registro {
		cc := a.deRegistro(c)
		if c.Pai != "" {
			pais[c.Pai].AddCommand(cc)
		} else {
			cc.GroupID = c.Grupo
			raiz.AddCommand(cc)
		}
	}
	raiz.AddCommand(a.cmdExport(), a.cmdImport(), a.cmdWatch(), a.cmdTop(), a.cmdShell(), a.cmdConsole(), a.cmdDev(), a.cmdBench(), a.cmdConfig(), a.cmdVersao(), a.cmdCompletar())
	return raiz
}

func abreviar(p string) string {
	if casa, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, casa) {
		return "~" + p[len(casa):]
	}
	return p
}

// marcaNegativo protege números negativos ("incr estoque:7 -3", "put saldo -5")
// do analisador de flags, que os confundiria com opções curtas. Nenhuma opção
// do Leme tem dígito como letra curta, então o número volta intacto no RunE.
const marcaNegativo = "\x1fneg:"

var numeroNegativo = regexp.MustCompile(`^-[0-9]+(\.[0-9]+)?$`)

func protegerNegativos(args []string) []string {
	out := make([]string, len(args))
	for i, v := range args {
		if numeroNegativo.MatchString(v) {
			v = marcaNegativo + v
		}
		out[i] = v
	}
	return out
}

func restaurarNegativo(v string) string { return strings.TrimPrefix(v, marcaNegativo) }

func (a *app) deRegistro(c *comandos.Comando) *cobra.Command {
	use := c.Nome
	if c.Uso != "" {
		use += " " + c.Uso
	}
	long := c.Resumo
	if c.Descricao != "" {
		long += "\n\n" + c.Descricao
	}
	ex := ""
	for _, e := range c.Exemplos {
		ex += "  " + e + "\n"
	}
	cmd := &cobra.Command{Use: use, Aliases: c.Apelidos, Short: c.Resumo, Long: long, Example: strings.TrimRight(ex, "\n")}
	max := c.Max
	cmd.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) < c.Min {
			return comandos.Uso("faltam argumentos. Uso: leme %s", c.Assinatura())
		}
		if max >= 0 && len(args) > max {
			return comandos.Uso("argumentos demais. Uso: leme %s", c.Assinatura())
		}
		return nil
	}
	for _, f := range c.Flags {
		switch f.Tipo {
		case comandos.FlagBooleano:
			cmd.Flags().BoolP(f.Nome, f.Curta, false, f.Ajuda)
		default:
			cmd.Flags().StringP(f.Nome, f.Curta, f.Padrao, f.Ajuda)
		}
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		pos := make([]string, len(args))
		for i, v := range args {
			pos[i] = restaurarNegativo(v)
		}
		args2 := comandos.Args{Pos: pos, Flags: map[string]string{}, Entrada: a.entrada}
		for _, f := range c.Flags {
			if fl := cmd.Flags().Lookup(f.Nome); fl != nil && fl.Changed {
				args2.Flags[f.Nome] = restaurarNegativo(fl.Value.String())
			}
		}
		s, err := a.sessao(cmd.Context())
		if err != nil {
			return err
		}
		ctx, cancelar := sinalizar(cmd.Context())
		defer cancelar()
		res, err := comandos.Executar(ctx, s, c, args2)
		if res != nil {
			f, ferr := a.formatoSaida()
			if ferr != nil {
				return ferr
			}
			saida.Escrever(a.saida, res, f, s.Estilo)
		}
		return err
	}
	return cmd
}

func (a *app) formatoSaida() (saida.Formato, error) {
	v := a.formato
	if v == "" {
		v = os.Getenv("LEME_SAIDA")
	}
	if v == "" {
		if p, ok := a.perfilAtual(); ok {
			v = p.Saida
		}
	}
	f, err := saida.ParseFormato(v)
	if err != nil {
		return "", comandos.Uso("%v", err)
	}
	return f, nil
}

func (a *app) perfilAtual() (config.Perfil, bool) {
	arq, err := config.Carregar(a.configArq)
	if err != nil {
		return config.Perfil{}, false
	}
	nome := a.perfil
	if nome == "" {
		nome = os.Getenv("LEME_PERFIL")
	}
	if nome != "" {
		p, ok := arq.Perfis[nome]
		return p, ok
	}
	return arq.Perfil("")
}

func (a *app) configCliente() (cliente.Config, error) {
	cfg := cliente.Config{}
	nomePerfil := a.perfil
	if nomePerfil == "" {
		nomePerfil = os.Getenv("LEME_PERFIL")
	}
	arq, err := config.Carregar(a.configArq)
	if err != nil {
		return cfg, err
	}
	p, ok := arq.Perfil(nomePerfil)
	if nomePerfil != "" && !ok {
		return cfg, comandos.Uso("perfil %q não existe (veja leme config list)", nomePerfil)
	}
	nos := a.nos
	if len(nos) == 0 && os.Getenv("LEME_NOS") != "" {
		nos = strings.Split(os.Getenv("LEME_NOS"), ",")
	}
	if len(nos) == 0 {
		nos = p.Nos
	}
	if len(nos) == 0 {
		nos = []string{"127.0.0.1:7400"}
	}
	cfg.Nos = nos
	token := a.token
	arqToken := a.tokenArquivo
	if token == "" && arqToken == "" {
		token = os.Getenv("LEME_TOKEN")
	}
	if token == "" && arqToken == "" {
		token, arqToken = p.Token, p.TokenArquivo
	}
	if token == "" && arqToken != "" {
		b, err := os.ReadFile(arqToken)
		if err != nil {
			return cfg, fmt.Errorf("ler token: %w", err)
		}
		token = strings.TrimSpace(string(b))
	}
	cfg.Token = token
	tl := transporte.ArquivosTLS{CA: primeiro(a.tlsCA, p.TLSCA), Certificado: primeiro(a.tlsCert, p.TLSCert), Chave: primeiro(a.tlsChave, p.TLSChave), Inseguro: a.tlsInseguro || p.TLSInseguro}
	tc, err := tl.Cliente()
	if err != nil {
		return cfg, err
	}
	cfg.TLS = tc
	cfg.TempoLimite = a.tempoLimite
	if cfg.TempoLimite == 0 && p.TempoLimite != "" {
		d, err := time.ParseDuration(p.TempoLimite)
		if err != nil {
			return cfg, comandos.Uso("tempo_limite inválido no perfil: %v", err)
		}
		cfg.TempoLimite = d
	}
	cons := a.consistencia
	if cons == "" {
		cons = os.Getenv("LEME_CONSISTENCIA")
	}
	if cons == "" {
		cons = p.Consistencia
	}
	c, err := protocolo.ParseConsistencia(cons)
	if err != nil {
		return cfg, comandos.Uso("%v", err)
	}
	cfg.Consistencia = c
	return cfg, nil
}

func primeiro(v ...string) string {
	for _, x := range v {
		if x != "" {
			return x
		}
	}
	return ""
}

func (a *app) conectar(ctx context.Context, cfg cliente.Config) (*cliente.Cliente, error) {
	cli, err := cliente.Novo(cfg)
	if err != nil {
		return nil, comandos.Uso("%v", err)
	}
	if err := cli.Conectar(ctx); err != nil {
		cli.Fechar()
		return nil, err
	}
	return cli, nil
}

func (a *app) sessao(ctx context.Context) (*comandos.Sessao, error) {
	cfg, err := a.configCliente()
	if err != nil {
		return nil, err
	}
	cli, err := a.conectar(ctx, cfg)
	if err != nil {
		return nil, err
	}
	a.cli = cli
	return &comandos.Sessao{Cliente: cli, Consistencia: cfg.Consistencia, Estilo: a.estilo()}, nil
}

func (a *app) rodarShell(ctx context.Context, arquivo string, pararNoErro bool) error {
	s, err := a.sessao(ctx)
	if err != nil {
		return err
	}
	f, err := a.formatoSaida()
	if err != nil {
		return err
	}
	entrada := a.arqEntrada
	if arquivo != "" {
		fh, err := os.Open(arquivo)
		if err != nil {
			return err
		}
		defer fh.Close()
		entrada = fh
	}
	ed := terminal.Novo(entrada, a.saida)
	r := &repl.REPL{Sessao: s, Formato: f, Editor: ed, Saida: a.saida, Erro: a.erro, PararNoErro: pararNoErro, Historico: filepath.Join(config.DiretorioDados(), "historico")}
	r.Reconectar = func(nos []string) (*cliente.Cliente, error) {
		cfg, err := a.configCliente()
		if err != nil {
			return nil, err
		}
		cfg.Nos = nos
		return a.conectar(ctx, cfg)
	}
	codigo := r.Rodar(ctx)
	a.cli = s.Cliente
	if codigo != 0 {
		return &codigoSaida{n: codigo}
	}
	return nil
}

func (a *app) cmdShell() *cobra.Command {
	var arquivo string
	var parar bool
	cmd := &cobra.Command{
		Use:     "shell",
		Aliases: []string{"repl", "interativo"},
		Short:   "Abre o modo interativo (ou executa um arquivo de comandos)",
		Long:    "Abre um terminal persistente conectado ao cluster, com histórico, autocompletar com Tab e comandos de sessão.\nCom --arquivo, ou com a entrada redirecionada, executa uma linha por vez sem prompt.",
		Example: "  leme shell\n  leme shell --arquivo carga.leme --parar-no-erro\n  printf 'put a 1\\nget a\\n' | leme shell",
		GroupID: "Ferramentas",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.rodarShell(cmd.Context(), arquivo, parar)
		},
	}
	cmd.Flags().StringVarP(&arquivo, "arquivo", "f", "", "executa os comandos de um arquivo, um por linha")
	cmd.Flags().BoolVar(&parar, "parar-no-erro", false, "no modo não interativo, para na primeira falha")
	return cmd
}

func (a *app) cmdVersao() *cobra.Command {
	return &cobra.Command{
		Use: "version", Aliases: []string{"versao"}, Short: "Mostra a versão do Leme", GroupID: "Ferramentas", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(a.saida, versao.Completa())
			return nil
		},
	}
}

func (a *app) cmdCompletar() *cobra.Command {
	return &cobra.Command{
		Use: "completion <bash|zsh|fish|powershell>", Aliases: []string{"completar"}, Short: "Gera o script de autocompletar para o shell", GroupID: "Ferramentas",
		Example:   "  leme completion bash > /etc/bash_completion.d/leme\n  leme completion powershell | Out-String | Invoke-Expression",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return a.raiz.GenBashCompletionV2(a.saida, true)
			case "zsh":
				return a.raiz.GenZshCompletion(a.saida)
			case "fish":
				return a.raiz.GenFishCompletion(a.saida, true)
			case "powershell":
				return a.raiz.GenPowerShellCompletionWithDesc(a.saida)
			}
			return comandos.Uso("shell desconhecido: %s (use bash, zsh, fish ou powershell)", args[0])
		},
	}
}
