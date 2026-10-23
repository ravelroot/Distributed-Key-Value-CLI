package no

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/anel"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/armazenamento"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/protocolo"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/versao"
)

var limitesHistograma = []float64{0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5}

type contadorOp struct {
	total    atomic.Uint64
	erros    atomic.Uint64
	baldes   [15]atomic.Uint64
	somaNs   atomic.Uint64
	mu       sync.Mutex
	recentes [512]time.Duration
	pos      int
	cheio    bool
}

type metricas struct {
	ops          sync.Map
	recusadas    atomic.Uint64
	falhasAuth   atomic.Uint64
	reparos      atomic.Uint64
	transferidas atomic.Uint64
	internas     atomic.Uint64
}

func novasMetricas() *metricas { return &metricas{} }

func (m *metricas) contador(op protocolo.Op) *contadorOp {
	v, _ := m.ops.LoadOrStore(op, &contadorOp{})
	return v.(*contadorOp)
}

func (m *metricas) registrar(op protocolo.Op, d time.Duration, err error) {
	c := m.contador(op)
	c.total.Add(1)
	if err != nil && protocolo.CodigoDe(err) != protocolo.CodigoNaoEncontrado {
		c.erros.Add(1)
	}
	c.somaNs.Add(uint64(d.Nanoseconds()))
	s := d.Seconds()
	i := sort.SearchFloat64s(limitesHistograma, s)
	c.baldes[i].Add(1)
	c.mu.Lock()
	c.recentes[c.pos] = d
	c.pos = (c.pos + 1) % len(c.recentes)
	if c.pos == 0 {
		c.cheio = true
	}
	c.mu.Unlock()
}

func (c *contadorOp) percentis() (p50, p95, p99 time.Duration) {
	c.mu.Lock()
	n := c.pos
	if c.cheio {
		n = len(c.recentes)
	}
	amostra := make([]time.Duration, n)
	copy(amostra, c.recentes[:n])
	c.mu.Unlock()
	if n == 0 {
		return
	}
	sort.Slice(amostra, func(i, j int) bool { return amostra[i] < amostra[j] })
	at := func(p float64) time.Duration { return amostra[int(p*float64(n-1))] }
	return at(0.50), at(0.95), at(0.99)
}

type EstatisticaOp struct {
	Op    string  `json:"op"`
	Total uint64  `json:"total"`
	Erros uint64  `json:"erros"`
	P50   float64 `json:"p50_ms"`
	P95   float64 `json:"p95_ms"`
	P99   float64 `json:"p99_ms"`
	Media float64 `json:"media_ms"`
}

type Estatisticas struct {
	ID               string                     `json:"id"`
	Endereco         string                     `json:"endereco"`
	Cluster          string                     `json:"cluster"`
	Versao           string                     `json:"versao"`
	Go               string                     `json:"go"`
	SO               string                     `json:"so"`
	Inicio           int64                      `json:"inicio"`
	TempoNoAr        float64                    `json:"tempo_no_ar_s"`
	Conexoes         int64                      `json:"conexoes"`
	ConexoesPares    int                        `json:"conexoes_pares"`
	Goroutines       int                        `json:"goroutines"`
	MemoriaBytes     uint64                     `json:"memoria_bytes"`
	TLS              bool                       `json:"tls"`
	Autenticacao     bool                       `json:"autenticacao"`
	Replicas         int                        `json:"replicas"`
	Armazenamento    armazenamento.Estatisticas `json:"armazenamento"`
	Operacoes        []EstatisticaOp            `json:"operacoes"`
	Dicas            int                        `json:"dicas_pendentes"`
	DicasEntregues   uint64                     `json:"dicas_entregues"`
	DicasDescartadas uint64                     `json:"dicas_descartadas"`
	Reparos          uint64                     `json:"reparos_de_leitura"`
	Transferidas     uint64                     `json:"chaves_transferidas"`
	Recusadas        uint64                     `json:"conexoes_recusadas"`
	FalhasAuth       uint64                     `json:"falhas_de_autenticacao"`
	Participacao     float64                    `json:"participacao_no_anel"`
}

func (n *No) Estatisticas() Estatisticas {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	pend, ent, desc := n.dicas.contagem()
	st := Estatisticas{
		ID: n.cfg.ID, Endereco: n.cfg.Anunciar, Cluster: n.cfg.Cluster, Versao: versao.Versao, Go: runtime.Version(), SO: runtime.GOOS + "/" + runtime.GOARCH,
		Inicio: n.inicio.UnixMilli(), TempoNoAr: time.Since(n.inicio).Seconds(), Conexoes: n.nConns.Load(), ConexoesPares: n.pool.Abertas(),
		Goroutines: runtime.NumGoroutine(), MemoriaBytes: mem.Alloc, TLS: n.tlsSrv != nil, Autenticacao: n.cfg.Token != "", Replicas: n.cfg.Replicas,
		Armazenamento: n.motor.Estatisticas(), Dicas: pend, DicasEntregues: ent, DicasDescartadas: desc, Reparos: n.metricas.reparos.Load(),
		Transferidas: n.metricas.transferidas.Load(), Recusadas: n.metricas.recusadas.Load(), FalhasAuth: n.metricas.falhasAuth.Load(),
		Participacao: n.anel.Participacao()[n.cfg.ID],
	}
	n.metricas.ops.Range(func(k, v any) bool {
		op := k.(protocolo.Op)
		c := v.(*contadorOp)
		p50, p95, p99 := c.percentis()
		total := c.total.Load()
		media := 0.0
		if total > 0 {
			media = float64(c.somaNs.Load()) / float64(total) / 1e6
		}
		st.Operacoes = append(st.Operacoes, EstatisticaOp{Op: op.String(), Total: total, Erros: c.erros.Load(), P50: ms(p50), P95: ms(p95), P99: ms(p99), Media: media})
		return true
	})
	sort.Slice(st.Operacoes, func(i, j int) bool { return st.Operacoes[i].Op < st.Operacoes[j].Op })
	return st
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

type TopologiaCompleta struct {
	protocolo.Topologia
	Participacao map[string]float64 `json:"participacao"`
	Pontos       []anel.Ponto       `json:"pontos"`
}

func (n *No) topologiaCompleta() TopologiaCompleta {
	return TopologiaCompleta{Topologia: n.topologia(), Participacao: n.anel.Participacao(), Pontos: n.anel.Pontos(256)}
}

func (n *No) iniciarHTTP() error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /saude", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusOK
		estado := "ok"
		if n.parado.Load() || n.saindo.Load() {
			status = http.StatusServiceUnavailable
			estado = "saindo"
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"status": estado, "id": n.cfg.ID, "versao": versao.Versao, "membros": len(n.membrosVivos())})
	})
	mux.HandleFunc("GET /v1/estatisticas", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(n.Estatisticas())
	})
	mux.HandleFunc("GET /v1/topologia", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(n.topologiaCompleta())
	})
	mux.HandleFunc("GET /metricas", n.prometheus)
	mux.HandleFunc("GET /metrics", n.prometheus)
	ln, err := net.Listen("tcp", n.cfg.Metricas)
	if err != nil {
		return fmt.Errorf("escutar métricas em %s: %w", n.cfg.Metricas, err)
	}
	n.http = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go n.http.Serve(ln)
	return nil
}

func (n *No) prometheus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	var b strings.Builder
	st := n.Estatisticas()
	rot := fmt.Sprintf(`no="%s",cluster="%s"`, st.ID, st.Cluster)
	g := func(nome, ajuda, tipo string, v any) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n%s{%s} %v\n", nome, ajuda, nome, tipo, nome, rot, v)
	}
	g("leme_tempo_no_ar_segundos", "Tempo desde o início do nó", "gauge", st.TempoNoAr)
	g("leme_chaves", "Chaves vivas no nó", "gauge", st.Armazenamento.Chaves)
	g("leme_lapides", "Lápides no nó", "gauge", st.Armazenamento.Lapides)
	g("leme_bytes_disco", "Bytes ocupados pelos segmentos", "gauge", st.Armazenamento.BytesDisco)
	g("leme_bytes_vivos", "Bytes dos registros vivos", "gauge", st.Armazenamento.BytesVivos)
	g("leme_segmentos", "Segmentos de log abertos", "gauge", st.Armazenamento.Segmentos)
	g("leme_conexoes", "Conexões de clientes e pares", "gauge", st.Conexoes)
	g("leme_dicas_pendentes", "Escritas aguardando réplicas fora do ar", "gauge", st.Dicas)
	g("leme_reparos_leitura_total", "Reparos de leitura enviados", "counter", st.Reparos)
	g("leme_compactacoes_total", "Compactações concluídas", "counter", st.Armazenamento.Compactacoes)
	g("leme_memoria_bytes", "Memória alocada pelo processo", "gauge", st.MemoriaBytes)
	b.WriteString("# HELP leme_operacoes_total Operações atendidas por tipo\n# TYPE leme_operacoes_total counter\n")
	b.WriteString("# HELP leme_operacao_segundos Latência das operações\n# TYPE leme_operacao_segundos histogram\n")
	n.metricas.ops.Range(func(k, v any) bool {
		op := strings.ToLower(k.(protocolo.Op).String())
		c := v.(*contadorOp)
		fmt.Fprintf(&b, "leme_operacoes_total{%s,op=\"%s\"} %d\n", rot, op, c.total.Load())
		fmt.Fprintf(&b, "leme_erros_total{%s,op=\"%s\"} %d\n", rot, op, c.erros.Load())
		var acumulado uint64
		for i, lim := range limitesHistograma {
			acumulado += c.baldes[i].Load()
			fmt.Fprintf(&b, "leme_operacao_segundos_bucket{%s,op=\"%s\",le=\"%g\"} %d\n", rot, op, lim, acumulado)
		}
		acumulado += c.baldes[len(limitesHistograma)].Load()
		fmt.Fprintf(&b, "leme_operacao_segundos_bucket{%s,op=\"%s\",le=\"+Inf\"} %d\n", rot, op, acumulado)
		fmt.Fprintf(&b, "leme_operacao_segundos_sum{%s,op=\"%s\"} %g\n", rot, op, float64(c.somaNs.Load())/1e9)
		fmt.Fprintf(&b, "leme_operacao_segundos_count{%s,op=\"%s\"} %d\n", rot, op, c.total.Load())
		return true
	})
	w.Write([]byte(b.String()))
}
