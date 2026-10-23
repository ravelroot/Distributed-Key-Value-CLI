package comandos

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/no"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/saida"
)

func init() {
	registrar(&Comando{
		Nome: "ping", Uso: "[nó]", Grupo: "Cluster", Min: 0, Max: 1,
		Resumo:   "Mede a latência até cada nó (ou só até um)",
		Exemplos: []string{"leme ping", "leme ping no-2"},
		Exec:     execPing,
	})
	registrar(&Comando{
		Nome: "locate", Apelidos: []string{"localizar"}, Uso: "<chave>", Grupo: "Cluster", Min: 1, Max: 1,
		Resumo:   "Mostra as réplicas responsáveis por uma chave",
		Exemplos: []string{"leme locate usuario:42"},
		Exec:     execLocate,
	})
	registrar(&Comando{
		Pai: "cluster", Nome: "status", Apelidos: []string{"estado", "resumo"}, Grupo: "Cluster", Min: 0, Max: 0,
		Resumo:   "Resumo do cluster: nós, chaves, disco, operações e latência",
		Exemplos: []string{"leme cluster status", "leme cluster status -o json"},
		Exec:     execStatus,
	})
	registrar(&Comando{
		Pai: "cluster", Nome: "nodes", Apelidos: []string{"nos", "membros"}, Grupo: "Cluster", Min: 0, Max: 0,
		Resumo: "Lista os membros com estado, endereço, latência e participação no anel",
		Exec:   execNodes,
	})
	registrar(&Comando{
		Pai: "cluster", Nome: "ring", Apelidos: []string{"anel"}, Grupo: "Cluster", Min: 0, Max: 0,
		Resumo: "Mostra como o espaço de chaves está dividido entre os nós",
		Exec:   execRing,
	})
	registrar(&Comando{
		Pai: "node", Nome: "stats", Apelidos: []string{"status", "estatisticas"}, Uso: "<nó>", Grupo: "Nós", Min: 1, Max: 1,
		Resumo:   "Estatísticas detalhadas de um nó (armazenamento, operações e latências)",
		Exemplos: []string{"leme node stats no-1", "leme node stats 10.0.0.12:7400 -o json"},
		Exec:     execNodeStats,
	})
	registrar(&Comando{
		Pai: "node", Nome: "compact", Apelidos: []string{"compactar"}, Uso: "<nó | todos>", Grupo: "Nós", Min: 1, Max: 1,
		Resumo:    "Compacta os segmentos de log do nó (remove versões antigas, lápides vencidas e chaves expiradas)",
		Descricao: "A compactação roda em segundo plano no nó e não bloqueia leituras nem escritas. Use todos para compactar o cluster inteiro, um nó por vez.",
		Exemplos:  []string{"leme node compact no-1", "leme node compact todos"},
		Exec:      execCompact,
	})
	registrar(&Comando{
		Pai: "node", Nome: "leave", Apelidos: []string{"sair", "remover"}, Uso: "<nó>", Grupo: "Nós", Min: 1, Max: 1, Perigoso: true,
		Resumo:    "Retira um nó do cluster depois de transferir as chaves dele",
		Descricao: "O nó anuncia a saída, o anel é recalculado e cada chave é enviada às novas réplicas. Depois disso o processo pode ser desligado.",
		Flags:     []Flag{{Nome: "confirmar", Curta: "y", Ajuda: "confirma a operação sem perguntar", Tipo: FlagBooleano}},
		Exemplos:  []string{"leme node leave no-3 --confirmar"},
		Exec:      execLeave,
	})
}

func execPing(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	alvo := "todos"
	if len(a.Pos) == 1 {
		alvo = a.Pos[0]
	}
	alvos, err := s.Cliente.Alvos(ctx, alvo)
	if err != nil {
		return nil, err
	}
	res := make([]map[string]any, len(alvos))
	g := &saida.Grade{Colunas: []string{"", "NÓ", "ENDEREÇO", "LATÊNCIA"}, Direita: []int{3}}
	var wg sync.WaitGroup
	pings := make([][]string, len(alvos))
	falhas := 0
	var mu sync.Mutex
	for i, end := range alvos {
		wg.Add(1)
		go func(i int, end string) {
			defer wg.Done()
			p := s.Cliente.Ping(ctx, end)
			estado, lat := "ok", saida.Duracao(p.Latencia)
			if p.Erro != "" {
				estado, lat = "erro", p.Erro
				mu.Lock()
				falhas++
				mu.Unlock()
			}
			pings[i] = []string{s.Estilo.Ponto(estado), p.ID, end, lat}
			res[i] = map[string]any{"endereco": end, "id": p.ID, "latencia_ms": float64(p.Latencia.Microseconds()) / 1000, "erro": p.Erro}
		}(i, end)
	}
	wg.Wait()
	g.Linhas = pings
	r := &saida.Resultado{Grade: g, Dados: res, Rodape: fmt.Sprintf("%d de %d nós responderam", len(alvos)-falhas, len(alvos))}
	if falhas > 0 && falhas == len(alvos) {
		return r, &ErroSaida{Codigo: SaidaIndisponivel, Msg: "nenhum nó respondeu"}
	}
	return r, nil
}

func execLocate(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	donos, err := s.Cliente.Donos(ctx, []byte(a.Pos[0]))
	if err != nil {
		return nil, err
	}
	g := &saida.Grade{Colunas: []string{"", "PAPEL", "NÓ", "ENDEREÇO", "ESTADO"}}
	var dados []map[string]any
	for i, d := range donos {
		papel := "réplica"
		if i == 0 {
			papel = "primária"
		}
		g.Linhas = append(g.Linhas, []string{s.Estilo.Ponto(d.Estado), papel, d.ID, d.Endereco, d.Estado})
		dados = append(dados, map[string]any{"ordem": i + 1, "papel": papel, "id": d.ID, "endereco": d.Endereco, "estado": d.Estado})
	}
	return &saida.Resultado{Grade: g, Dados: map[string]any{"chave": a.Pos[0], "replicas": dados}}, nil
}

type Resumo struct {
	Cluster       string            `json:"cluster"`
	Replicas      int               `json:"replicas"`
	VNodes        int               `json:"vnodes"`
	Membros       int               `json:"membros"`
	Vivos         int               `json:"vivos"`
	Suspeitos     int               `json:"suspeitos"`
	Mortos        int               `json:"mortos"`
	ChavesFisicas int               `json:"chaves_fisicas"`
	ChavesLogicas int               `json:"chaves_logicas"`
	BytesDisco    int64             `json:"bytes_disco"`
	BytesVivos    int64             `json:"bytes_vivos"`
	Operacoes     uint64            `json:"operacoes"`
	Erros         uint64            `json:"erros"`
	P99Get        float64           `json:"p99_get_ms"`
	P99Put        float64           `json:"p99_put_ms"`
	Dicas         int               `json:"dicas_pendentes"`
	Reparos       uint64            `json:"reparos_de_leitura"`
	Saude         string            `json:"saude"`
	Nos           []no.Estatisticas `json:"nos"`
	Falhas        []string          `json:"falhas,omitempty"`
}

func ColetarResumo(ctx context.Context, s *Sessao) (Resumo, error) {
	t, err := s.Cliente.Topologia(ctx)
	if err != nil {
		return Resumo{}, err
	}
	r := Resumo{Cluster: t.Cluster, Replicas: t.Replicas, VNodes: t.VNodes}
	for _, m := range t.Membros {
		if m.Estado == "saiu" {
			continue
		}
		r.Membros++
		switch m.Estado {
		case "vivo":
			r.Vivos++
		case "suspeito":
			r.Suspeitos++
		default:
			r.Mortos++
		}
	}
	sts, errs := s.Cliente.TodasEstatisticas(ctx)
	for _, e := range errs {
		r.Falhas = append(r.Falhas, e.Error())
	}
	for _, st := range sts {
		r.ChavesFisicas += st.Armazenamento.Chaves
		r.BytesDisco += st.Armazenamento.BytesDisco
		r.BytesVivos += st.Armazenamento.BytesVivos
		r.Dicas += st.Dicas
		r.Reparos += st.Reparos
		for _, op := range st.Operacoes {
			if strings.HasPrefix(op.Op, "REPLICA") || op.Op == "GOSSIP" || op.Op == "SCAN_LOCAL" || op.Op == "ENTRAR" {
				continue
			}
			r.Operacoes += op.Total
			r.Erros += op.Erros
			if op.Op == "GET" && op.P99 > r.P99Get {
				r.P99Get = op.P99
			}
			if op.Op == "PUT" && op.P99 > r.P99Put {
				r.P99Put = op.P99
			}
		}
	}
	sort.Slice(sts, func(i, j int) bool { return sts[i].ID < sts[j].ID })
	r.Nos = sts
	rep := r.Replicas
	if rep > r.Membros && r.Membros > 0 {
		rep = r.Membros
	}
	if rep > 0 {
		r.ChavesLogicas = r.ChavesFisicas / rep
	}
	quorum := rep/2 + 1
	switch {
	case r.Vivos == r.Membros:
		r.Saude = "saudável"
	case r.Vivos >= quorum:
		r.Saude = "degradado"
	default:
		r.Saude = "sem quórum"
	}
	return r, nil
}

func execStatus(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	r, err := ColetarResumo(ctx, s)
	if err != nil {
		return nil, err
	}
	estado := "ok"
	if r.Saude == "degradado" {
		estado = "aviso"
	} else if r.Saude == "sem quórum" {
		estado = "erro"
	}
	pares := []saida.Par{
		{Rotulo: "cluster", Valor: r.Cluster},
		{Rotulo: "saúde", Valor: s.Estilo.Ponto(estado) + " " + r.Saude},
		{Rotulo: "nós", Valor: fmt.Sprintf("%d vivos · %d suspeitos · %d fora do ar", r.Vivos, r.Suspeitos, r.Mortos)},
		{Rotulo: "replicação", Valor: fmt.Sprintf("%d cópias · quórum %d · %d vnodes por nó", r.Replicas, r.Replicas/2+1, r.VNodes)},
		{Rotulo: "chaves", Valor: fmt.Sprintf("≈ %d (%d cópias no total)", r.ChavesLogicas, r.ChavesFisicas)},
		{Rotulo: "disco", Valor: fmt.Sprintf("%s em segmentos · %s vivos", saida.Bytes(r.BytesDisco), saida.Bytes(r.BytesVivos))},
		{Rotulo: "operações", Valor: fmt.Sprintf("%d atendidas · %d erros", r.Operacoes, r.Erros)},
		{Rotulo: "latência p99", Valor: fmt.Sprintf("get %.2f ms · put %.2f ms", r.P99Get, r.P99Put)},
		{Rotulo: "pendências", Valor: fmt.Sprintf("%d dicas · %d reparos de leitura", r.Dicas, r.Reparos)},
	}
	res := &saida.Resultado{Pares: pares, Dados: r}
	if len(r.Falhas) > 0 {
		res.Rodape = "sem resposta: " + strings.Join(r.Falhas, "; ")
	}
	return res, nil
}

func execNodes(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	t, err := s.Cliente.Topologia(ctx)
	if err != nil {
		return nil, err
	}
	g := &saida.Grade{Colunas: []string{"", "NÓ", "ENDEREÇO", "ESTADO", "NO AR", "LATÊNCIA", "ANEL", "VERSÃO"}, Direita: []int{4, 5, 6}}
	for _, m := range t.Membros {
		noAr := "—"
		if m.Estado != "saiu" && m.Desde > 0 {
			noAr = saida.Duracao(time.Since(time.UnixMilli(m.Desde)).Round(time.Second))
		}
		lat := "—"
		if m.ID == t.Origem {
			lat = "local"
		} else if m.Latencia > 0 {
			lat = saida.Duracao(time.Duration(m.Latencia) * time.Microsecond)
		}
		g.Linhas = append(g.Linhas, []string{s.Estilo.Ponto(m.Estado), m.ID, m.Endereco, m.Estado, noAr, lat, fmt.Sprintf("%.1f%%", t.Participacao[m.ID]*100), m.Versao})
	}
	return &saida.Resultado{Grade: g, Dados: t, Rodape: fmt.Sprintf("cluster %s · visto por %s", t.Cluster, t.Origem)}, nil
}

func barra(f float64, largura int) string {
	n := int(f*float64(largura) + 0.5)
	if n > largura {
		n = largura
	}
	return strings.Repeat("█", n) + strings.Repeat("░", largura-n)
}

func execRing(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	t, err := s.Cliente.Topologia(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	for id := range t.Participacao {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	maior := 0.0
	for _, id := range ids {
		if t.Participacao[id] > maior {
			maior = t.Participacao[id]
		}
	}
	g := &saida.Grade{Colunas: []string{"NÓ", "FAIXA DO ANEL", "", "VNODES"}, Direita: []int{1, 3}}
	for _, id := range ids {
		p := t.Participacao[id]
		rel := 0.0
		if maior > 0 {
			rel = p / maior
		}
		g.Linhas = append(g.Linhas, []string{id, fmt.Sprintf("%.2f%%", p*100), s.Estilo.Fraco(barra(rel, 24)), strconv.Itoa(t.VNodes)})
	}
	return &saida.Resultado{Grade: g, Dados: map[string]any{"vnodes": t.VNodes, "replicas": t.Replicas, "participacao": t.Participacao}, Rodape: fmt.Sprintf("hash FNV-1a 64 com mistura · %d vnodes por nó · cada chave vai para %d nós seguidos no anel", t.VNodes, t.Replicas)}, nil
}

func execNodeStats(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	alvos, err := s.Cliente.Alvos(ctx, a.Pos[0])
	if err != nil {
		return nil, err
	}
	st, err := s.Cliente.Estatisticas(ctx, alvos[0])
	if err != nil {
		return nil, err
	}
	ar := st.Armazenamento
	ultima := "nunca"
	if ar.UltimaCompactacao > 0 {
		ultima = time.UnixMilli(ar.UltimaCompactacao).Format("02/01/2006 15:04")
	}
	pares := []saida.Par{
		{Rotulo: "nó", Valor: fmt.Sprintf("%s · %s", st.ID, st.Endereco)},
		{Rotulo: "versão", Valor: fmt.Sprintf("Leme %s · %s · %s", st.Versao, st.Go, st.SO)},
		{Rotulo: "no ar", Valor: saida.Duracao(time.Duration(st.TempoNoAr * float64(time.Second)).Round(time.Second))},
		{Rotulo: "segurança", Valor: fmt.Sprintf("TLS %s · token %s", simNao(st.TLS), simNao(st.Autenticacao))},
		{Rotulo: "conexões", Valor: fmt.Sprintf("%d abertas · %d com pares · %d recusadas", st.Conexoes, st.ConexoesPares, st.Recusadas)},
		{Rotulo: "processo", Valor: fmt.Sprintf("%s de memória · %d goroutines", saida.Bytes(int64(st.MemoriaBytes)), st.Goroutines)},
		{Rotulo: "anel", Valor: fmt.Sprintf("%.2f%% do espaço de chaves", st.Participacao*100)},
		{Rotulo: "chaves", Valor: fmt.Sprintf("%d vivas · %d lápides · %d expiradas", ar.Chaves, ar.Lapides, ar.Expiradas)},
		{Rotulo: "disco", Valor: fmt.Sprintf("%s em %d segmentos · %s vivos · fragmentação %.0f%%", saida.Bytes(ar.BytesDisco), ar.Segmentos, saida.Bytes(ar.BytesVivos), ar.Fragmentacao*100)},
		{Rotulo: "fsync", Valor: ar.Sincronizar},
		{Rotulo: "compactação", Valor: fmt.Sprintf("%d feitas · última %s", ar.Compactacoes, ultima)},
		{Rotulo: "replicação", Valor: fmt.Sprintf("%d dicas pendentes · %d entregues · %d reparos · %d chaves transferidas", st.Dicas, st.DicasEntregues, st.Reparos, st.Transferidas)},
	}
	g := &saida.Grade{Colunas: []string{"OPERAÇÃO", "TOTAL", "ERROS", "MÉDIA", "P50", "P95", "P99"}, Direita: []int{1, 2, 3, 4, 5, 6}}
	for _, op := range st.Operacoes {
		g.Linhas = append(g.Linhas, []string{op.Op, strconv.FormatUint(op.Total, 10), strconv.FormatUint(op.Erros, 10), fmt.Sprintf("%.2f", op.Media), fmt.Sprintf("%.2f", op.P50), fmt.Sprintf("%.2f", op.P95), fmt.Sprintf("%.2f", op.P99)})
	}
	return &saida.Resultado{Pares: pares, Grade: g, Dados: st, Rodape: "latências em milissegundos, sobre as últimas 512 operações de cada tipo"}, nil
}

func simNao(b bool) string {
	if b {
		return "ativo"
	}
	return "desligado"
}

func execCompact(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	alvos, err := s.Cliente.Alvos(ctx, a.Pos[0])
	if err != nil {
		return nil, err
	}
	g := &saida.Grade{Colunas: []string{"", "NÓ", "ANTES", "DEPOIS", "REMOVIDOS", "TEMPO"}, Direita: []int{2, 3, 4, 5}}
	var dados []map[string]any
	var falhou error
	for _, end := range alvos {
		r, err := s.Cliente.Compactar(ctx, end)
		if err != nil {
			g.Linhas = append(g.Linhas, []string{s.Estilo.Ponto("erro"), end, "—", "—", "—", err.Error()})
			falhou = err
			continue
		}
		antes, _ := r["bytes_antes"].(float64)
		depois, _ := r["bytes_depois"].(float64)
		rem, _ := r["registros_removidos"].(float64)
		dur, _ := r["duracao_ns"].(float64)
		g.Linhas = append(g.Linhas, []string{s.Estilo.Ponto("ok"), end, saida.Bytes(int64(antes)), saida.Bytes(int64(depois)), strconv.Itoa(int(rem)), saida.Duracao(time.Duration(dur))})
		r["endereco"] = end
		dados = append(dados, r)
	}
	return &saida.Resultado{Grade: g, Dados: dados}, falhou
}

func execLeave(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	if !a.Bool("confirmar") {
		return nil, Uso("retirar um nó move as chaves dele para outros nós. Repita com --confirmar")
	}
	alvos, err := s.Cliente.Alvos(ctx, a.Pos[0])
	if err != nil {
		return nil, err
	}
	if err := s.Cliente.Sair(ctx, alvos[0]); err != nil {
		return nil, err
	}
	return &saida.Resultado{Mensagem: fmt.Sprintf("%s  %s", s.Estilo.Verde("OK"), s.Estilo.Fraco("o nó "+a.Pos[0]+" está saindo e transferindo as chaves; acompanhe com cluster nodes")), Texto: "OK", Dados: map[string]any{"no": a.Pos[0], "saindo": true}}, nil
}
