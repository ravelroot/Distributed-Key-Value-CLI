package comandos

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/saida"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/versao"
)

// Verificacao é o resultado de um item do diagnóstico.
type Verificacao struct {
	Nome     string `json:"nome"`
	Estado   string `json:"estado"`
	Detalhe  string `json:"detalhe"`
	Sugestao string `json:"sugestao,omitempty"`
}

const (
	EstadoOK    = "ok"
	EstadoAviso = "aviso"
	EstadoFalha = "falha"
)

// Limites usados pelo diagnóstico.
const (
	limiteP99Aviso   = 50.0
	limiteP99Falha   = 250.0
	limiteFragAviso  = 0.5
	limiteFragBytes  = 1 << 20
	limiteDicasFalha = 1000
	limiteDesvioAnel = 0.25
)

func init() {
	registrar(&Comando{
		Nome: "doctor", Apelidos: []string{"diagnostico", "diagnóstico"}, Grupo: "Cluster", Min: 0, Max: 0,
		Resumo:    "Diagnostica o cluster e sugere o que fazer",
		Descricao: "Confere a conexão com os nós semente, o token, os nós vivos, se o quórum é possível, o equilíbrio do anel, as dicas pendentes, a fragmentação do disco, a latência p99, a taxa de erros, as versões dos nós e o uso de TLS e token. Sai com código 8 quando alguma verificação falha.",
		Exemplos:  []string{"leme doctor", "leme doctor -o json | jq '.[] | select(.estado != \"ok\")'"},
		Exec:      execDoctor,
	})
}

// Diagnosticar roda todas as verificações e nunca devolve erro: falhas de
// conexão viram verificações com estado "falha".
func Diagnosticar(ctx context.Context, s *Sessao) []Verificacao {
	var vs []Verificacao
	add := func(nome, estado, detalhe, sugestao string) {
		vs = append(vs, Verificacao{Nome: nome, Estado: estado, Detalhe: detalhe, Sugestao: sugestao})
	}

	sementes := s.Cliente.Config().Nos
	respondem := 0
	var semResposta []string
	for _, end := range sementes {
		p := s.Cliente.Ping(ctx, end)
		if p.Erro == "" {
			respondem++
		} else {
			semResposta = append(semResposta, end)
		}
	}
	switch {
	case len(sementes) == 0:
		add("Nós semente", EstadoFalha, "nenhum nó informado", "use --nos, LEME_NOS ou um perfil salvo com leme config set")
	case respondem == 0:
		add("Nós semente", EstadoFalha, fmt.Sprintf("nenhum dos %d nós informados respondeu", len(sementes)), "confira se os nós estão no ar, o endereço e a porta, e se há firewall no caminho")
		return vs
	case respondem < len(sementes):
		add("Nós semente", EstadoAviso, fmt.Sprintf("%d de %d responderam; sem resposta: %s", respondem, len(sementes), strings.Join(semResposta, ", ")), "confira se esses nós estão no ar; se saíram do cluster, tire-os do perfil")
	default:
		add("Nós semente", EstadoOK, fmt.Sprintf("%d de %d responderam", respondem, len(sementes)), "")
	}

	r, err := ColetarResumo(ctx, s)
	if err != nil {
		if CodigoSaida(err) == SaidaAutenticacao {
			add("Token", EstadoFalha, "o cluster recusou o token", "confira --token, --token-arquivo ou LEME_TOKEN")
		} else {
			add("Topologia", EstadoFalha, err.Error(), "tente de novo com --tempo-limite maior")
		}
		return vs
	}
	add("Token", EstadoOK, "aceito pelo cluster", "")

	switch {
	case r.Mortos == 0 && r.Suspeitos == 0:
		add("Membros", EstadoOK, fmt.Sprintf("%d de %d nós vivos", r.Vivos, r.Membros), "")
	case r.Mortos == 0:
		add("Membros", EstadoAviso, fmt.Sprintf("%d vivos, %d %s", r.Vivos, r.Suspeitos, plural(uint64(r.Suspeitos), "suspeito", "suspeitos")), "um nó suspeito parou de responder ao gossip; veja os logs dele")
	default:
		add("Membros", EstadoFalha, fmt.Sprintf("%d vivos, %d suspeitos, %d fora do ar", r.Vivos, r.Suspeitos, r.Mortos), "religue o nó ou retire-o do cluster com leme node leave")
	}

	rep := r.Replicas
	if rep > r.Membros && r.Membros > 0 {
		rep = r.Membros
	}
	quorum := rep/2 + 1
	if r.Vivos >= quorum {
		add("Quórum", EstadoOK, fmt.Sprintf("%d cópias por chave, quórum de %d", rep, quorum), "")
	} else {
		add("Quórum", EstadoFalha, fmt.Sprintf("só %d nós vivos para quórum de %d", r.Vivos, quorum), "use -c um para ler o que ainda está disponível enquanto os nós voltam")
	}
	if r.Membros < r.Replicas {
		add("Replicação", EstadoAviso, fmt.Sprintf("%d nós para %d cópias configuradas", r.Membros, r.Replicas), "suba mais nós ou reduza LEME_REPLICAS")
	}

	if t, err := s.Cliente.Topologia(ctx); err == nil && len(t.Participacao) > 1 {
		ideal := 1 / float64(len(t.Participacao))
		pior, quem := 0.0, ""
		for id, p := range t.Participacao {
			if d := math.Abs(p-ideal) / ideal; d > pior {
				pior, quem = d, id
			}
		}
		if pior > limiteDesvioAnel {
			add("Equilíbrio do anel", EstadoAviso, fmt.Sprintf("%s está %.0f%% fora da fatia ideal", quem, pior*100), "aumente LEME_VNODES para distribuir melhor as chaves")
		} else {
			add("Equilíbrio do anel", EstadoOK, fmt.Sprintf("maior desvio de %.0f%% da fatia ideal", pior*100), "")
		}
	}

	for _, f := range r.Falhas {
		add("Estatísticas", EstadoFalha, f, "o nó está no anel mas não respondeu; confira se o processo está no ar ou sobrecarregado")
	}

	switch {
	case r.Dicas == 0:
		add("Dicas pendentes", EstadoOK, "nenhuma escrita aguardando réplicas", "")
	case r.Dicas > limiteDicasFalha:
		add("Dicas pendentes", EstadoFalha, fmt.Sprintf("%d escritas aguardando réplicas fora do ar", r.Dicas), "religue as réplicas fora do ar; as dicas são entregues assim que voltam")
	default:
		add("Dicas pendentes", EstadoAviso, fmt.Sprintf("%d escritas aguardando réplicas", r.Dicas), "normal logo depois de um nó voltar; deve zerar em segundos")
	}

	var fragmentados []string
	for _, st := range r.Nos {
		a := st.Armazenamento
		if a.BytesDisco > limiteFragBytes && a.Fragmentacao > limiteFragAviso {
			fragmentados = append(fragmentados, fmt.Sprintf("%s %.0f%%", st.ID, a.Fragmentacao*100))
		}
	}
	if len(fragmentados) > 0 {
		add("Fragmentação", EstadoAviso, strings.Join(fragmentados, ", "), "rode leme node compact todos fora do horário de pico")
	} else {
		add("Fragmentação", EstadoOK, "abaixo de 50% em todos os nós", "")
	}

	p99 := math.Max(r.P99Get, r.P99Put)
	detalheP99 := fmt.Sprintf("leitura %s ms, escrita %s ms (pior nó)", decimal(r.P99Get, 2), decimal(r.P99Put, 2))
	switch {
	case p99 > limiteP99Falha:
		add("Latência p99", EstadoFalha, detalheP99, "veja disco e rede dos nós; LEME_SINCRONIZAR=sempre deixa as escritas mais lentas")
	case p99 > limiteP99Aviso:
		add("Latência p99", EstadoAviso, detalheP99, "acompanhe com leme top; picos depois de compactação são esperados")
	default:
		add("Latência p99", EstadoOK, detalheP99, "")
	}

	if r.Operacoes > 0 {
		taxa := float64(r.Erros) / float64(r.Operacoes)
		detalhe := fmt.Sprintf("%d %s em %d operações (%s%%)", r.Erros, plural(r.Erros, "erro", "erros"), r.Operacoes, decimal(taxa*100, 2))
		if taxa > 0.01 {
			add("Erros", EstadoAviso, detalhe, "erros de conflito e chave ausente contam aqui; veja leme node stats")
		} else {
			add("Erros", EstadoOK, detalhe, "")
		}
	}

	versoes := map[string]bool{}
	tls, token := 0, 0
	for _, st := range r.Nos {
		versoes[st.Versao] = true
		if st.TLS {
			tls++
		}
		if st.Autenticacao {
			token++
		}
	}
	lista := make([]string, 0, len(versoes))
	for v := range versoes {
		lista = append(lista, v)
	}
	sort.Strings(lista)
	if len(lista) > 1 {
		add("Versões", EstadoAviso, "nós com versões diferentes: "+strings.Join(lista, ", "), "atualize um nó por vez até todos ficarem na mesma versão")
	} else if len(lista) == 1 {
		det := "todos os nós na " + lista[0]
		if lista[0] != versao.Versao {
			det += ", CLI na " + versao.Versao
		}
		add("Versões", EstadoOK, det, "")
	}
	if n := len(r.Nos); n > 0 {
		if tls < n || token < n {
			add("Segurança", EstadoAviso, fmt.Sprintf("TLS em %d de %d nós, token em %d de %d", tls, n, token, n), "fora da máquina local, use --tls-cert/--tls-chave e --token-arquivo")
		} else {
			add("Segurança", EstadoOK, "TLS e token em todos os nós", "")
		}
	}
	return vs
}

// decimal formata no padrão brasileiro (vírgula), já que o detalhe é texto.
func decimal(f float64, casas int) string {
	return strings.Replace(strconv.FormatFloat(f, 'f', casas, 64), ".", ",", 1)
}

func plural(n uint64, um, varios string) string {
	if n == 1 {
		return um
	}
	return varios
}

func execDoctor(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	vs := Diagnosticar(ctx, s)
	g := &saida.Grade{Colunas: []string{"", "VERIFICAÇÃO", "RESULTADO"}}
	cont := map[string]int{}
	var sugestoes []string
	for _, v := range vs {
		cont[v.Estado]++
		ponto := v.Estado
		if ponto == EstadoFalha {
			ponto = "erro"
		}
		g.Linhas = append(g.Linhas, []string{s.Estilo.Ponto(ponto), v.Nome, v.Detalhe})
		if v.Sugestao != "" && v.Estado != EstadoOK {
			sugestoes = append(sugestoes, fmt.Sprintf("  %s: %s", v.Nome, v.Sugestao))
		}
	}
	rodape := fmt.Sprintf("%d ok · %d %s · %d %s", cont[EstadoOK], cont[EstadoAviso], plural(uint64(cont[EstadoAviso]), "aviso", "avisos"), cont[EstadoFalha], plural(uint64(cont[EstadoFalha]), "falha", "falhas"))
	if len(sugestoes) > 0 {
		rodape += "\n\nO que fazer:\n" + strings.Join(sugestoes, "\n")
	}
	r := &saida.Resultado{Grade: g, Dados: vs, Rodape: rodape}
	if cont[EstadoFalha] > 0 {
		return r, &ErroSaida{Codigo: SaidaDiagnostico, Msg: fmt.Sprintf("o diagnóstico encontrou %d falhas", cont[EstadoFalha])}
	}
	return r, nil
}
