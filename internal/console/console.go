package console

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/anel"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/cliente"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/comandos"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/protocolo"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/saida"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/versao"
)

//go:embed web
var arquivos embed.FS

//go:embed openapi.json
var especificacao []byte

type Config struct {
	Cliente      *cliente.Cliente
	Consistencia protocolo.Consistencia
	Endereco     string
	Token        string
}

type Servidor struct {
	cfg  Config
	http *http.Server
	ln   net.Listener
}

const cookie = "leme_console"

func Novo(cfg Config) (*Servidor, error) {
	if cfg.Endereco == "" {
		cfg.Endereco = "127.0.0.1:8080"
	}
	host, _, err := net.SplitHostPort(cfg.Endereco)
	if err != nil {
		return nil, fmt.Errorf("endereço inválido %q: %w", cfg.Endereco, err)
	}
	ip := net.ParseIP(host)
	local := host == "localhost" || (ip != nil && ip.IsLoopback())
	if !local && cfg.Token == "" {
		return nil, errors.New("para expor o console fora de 127.0.0.1 defina --token-console")
	}
	return &Servidor{cfg: cfg}, nil
}

func (s *Servidor) URL() string {
	end := s.cfg.Endereco
	if s.ln != nil {
		end = s.ln.Addr().String()
	}
	host, porta, _ := net.SplitHostPort(end)
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	u := "http://" + net.JoinHostPort(host, porta) + "/"
	if s.cfg.Token != "" {
		u += "?token=" + s.cfg.Token
	}
	return u
}

func (s *Servidor) Handler() http.Handler {
	mux := http.NewServeMux()
	estatico, _ := fs.Sub(arquivos, "web")
	arquivosHTTP := http.FileServer(http.FS(estatico))
	mux.Handle("GET /", s.autenticado(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/fontes/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		arquivosHTTP.ServeHTTP(w, r)
	}, true))
	mux.Handle("GET /api/v1/saude", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		responder(w, http.StatusOK, map[string]any{"status": "ok", "versao": versao.Versao})
	}))
	mux.Handle("GET /api/v1/resumo", s.autenticado(s.resumo, false))
	mux.Handle("GET /api/v1/topologia", s.autenticado(s.topologia, false))
	mux.Handle("GET /api/v1/chaves", s.autenticado(s.chaves, false))
	mux.Handle("GET /api/v1/chave", s.autenticado(s.lerChave, false))
	mux.Handle("PUT /api/v1/chave", s.autenticado(s.gravarChave, false))
	mux.Handle("DELETE /api/v1/chave", s.autenticado(s.apagarChave, false))
	mux.Handle("POST /api/v1/executar", s.autenticado(s.executar, false))
	mux.Handle("GET /api/v1/comandos", s.autenticado(s.listaComandos, false))
	mux.Handle("GET /api/v1/localizar", s.autenticado(s.localizar, false))
	mux.Handle("GET /api/v1/exportar", s.autenticado(s.exportar, false))
	mux.Handle("POST /api/v1/importar", s.autenticado(s.importar, false))
	mux.Handle("GET /api/v1/diagnostico", s.autenticado(s.diagnostico, false))
	mux.Handle("GET /api/v1/integracao", s.autenticado(s.integracao, false))
	mux.Handle("GET /api/v1/openapi.json", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(especificacao)
	}))
	return s.seguranca(mux)
}

// portador devolve verdadeiro quando o pedido traz Authorization: Bearer com o
// token do console. O navegador nunca envia esse cabeçalho sozinho, então um
// pedido assim não pode ter sido forjado por outra página (sem CSRF).
func (s *Servidor) portador(r *http.Request) bool {
	a := r.Header.Get("Authorization")
	if s.cfg.Token == "" || !strings.HasPrefix(a, "Bearer ") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(a, "Bearer ")), []byte(s.cfg.Token)) == 1
}

func (s *Servidor) seguranca(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cab := w.Header()
		cab.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; style-src-attr 'unsafe-inline'; script-src 'self'; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		cab.Set("X-Content-Type-Options", "nosniff")
		cab.Set("X-Frame-Options", "DENY")
		cab.Set("Referrer-Policy", "no-referrer")
		cab.Set("Cross-Origin-Opener-Policy", "same-origin")
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Leme") != "1" && !s.portador(r) {
			responder(w, http.StatusForbidden, map[string]any{"erro": "cabeçalho X-Leme ausente"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 33<<20)
		h.ServeHTTP(w, r)
	})
}

func (s *Servidor) autenticado(h http.HandlerFunc, pagina bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Token == "" {
			h(w, r)
			return
		}
		if t := r.URL.Query().Get("token"); t != "" && pagina {
			if subtle.ConstantTimeCompare([]byte(t), []byte(s.cfg.Token)) == 1 {
				http.SetCookie(w, &http.Cookie{Name: cookie, Value: s.cfg.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil})
				http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
				return
			}
		}
		recebido := ""
		if c, err := r.Cookie(cookie); err == nil {
			recebido = c.Value
		}
		if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
			recebido = strings.TrimPrefix(a, "Bearer ")
		}
		if subtle.ConstantTimeCompare([]byte(recebido), []byte(s.cfg.Token)) != 1 {
			if pagina {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte("Abra o endereço completo mostrado pelo comando leme console (com ?token=...)."))
				return
			}
			responder(w, http.StatusUnauthorized, map[string]any{"erro": "não autorizado"})
			return
		}
		h(w, r)
	})
}

func (s *Servidor) Iniciar() error {
	ln, err := net.Listen("tcp", s.cfg.Endereco)
	if err != nil {
		return fmt.Errorf("escutar em %s: %w", s.cfg.Endereco, err)
	}
	s.ln = ln
	s.http = &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 2 * time.Minute}
	go s.http.Serve(ln)
	return nil
}

func (s *Servidor) Parar(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}

func responder(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	enc.Encode(v)
}

func status(err error) int {
	switch protocolo.CodigoDe(err) {
	case protocolo.CodigoNaoEncontrado:
		return http.StatusNotFound
	case protocolo.CodigoRequisicaoInvalida:
		return http.StatusBadRequest
	case protocolo.CodigoConflito:
		return http.StatusConflict
	case protocolo.CodigoNaoAutorizado:
		return http.StatusUnauthorized
	case protocolo.CodigoQuorum, protocolo.CodigoIndisponivel, protocolo.CodigoTempoEsgotado:
		return http.StatusServiceUnavailable
	}
	var eu *comandos.ErroUso
	if errors.As(err, &eu) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func falha(w http.ResponseWriter, err error) {
	responder(w, status(err), map[string]any{"erro": err.Error(), "codigo": protocolo.CodigoDe(err).String()})
}

func (s *Servidor) sessao() *comandos.Sessao {
	return &comandos.Sessao{Cliente: s.cfg.Cliente, Consistencia: s.cfg.Consistencia}
}

func (s *Servidor) resumo(w http.ResponseWriter, r *http.Request) {
	ctx, cancelar := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancelar()
	res, err := comandos.ColetarResumo(ctx, s.sessao())
	if err != nil {
		falha(w, err)
		return
	}
	cons := s.cfg.Consistencia.String()
	if s.cfg.Consistencia == protocolo.ConsistenciaPadrao {
		cons = "quorum"
	}
	responder(w, http.StatusOK, map[string]any{"resumo": res, "versao": versao.Versao, "agora": time.Now().UnixMilli(), "consistencia": cons})
}

func (s *Servidor) topologia(w http.ResponseWriter, r *http.Request) {
	t, err := s.cfg.Cliente.Topologia(r.Context())
	if err != nil {
		falha(w, err)
		return
	}
	responder(w, http.StatusOK, t)
}

type itemJSON struct {
	Chave   string `json:"chave"`
	Valor   string `json:"valor,omitempty"`
	Binario bool   `json:"binario,omitempty"`
	Tamanho int    `json:"tamanho"`
	Versao  string `json:"versao"`
	Gravado int64  `json:"gravado"`
	Expira  int64  `json:"expira,omitempty"`
}

func paraJSON(chave, valor []byte, versao uint64, expira int64, resumir bool) itemJSON {
	it := itemJSON{Chave: string(chave), Tamanho: len(valor), Versao: strconv.FormatUint(versao, 10), Gravado: int64(versao >> 16), Expira: expira}
	if utf8.Valid(valor) {
		v := string(valor)
		if resumir && utf8.RuneCountInString(v) > 160 {
			v = string([]rune(v)[:160]) + "…"
		}
		it.Valor = v
	} else {
		it.Binario = true
	}
	return it
}

func (s *Servidor) chaves(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limite, _ := strconv.Atoi(q.Get("limite"))
	if limite <= 0 || limite > 500 {
		limite = 50
	}
	itens, mais, err := s.cfg.Cliente.Scan(r.Context(), cliente.OpcoesScan{Prefixo: q.Get("prefixo"), Depois: q.Get("depois"), Limite: limite})
	if err != nil {
		falha(w, err)
		return
	}
	out := make([]itemJSON, 0, len(itens))
	for _, it := range itens {
		out = append(out, paraJSON(it.Chave, it.Valor, it.Versao, it.Expira, true))
	}
	responder(w, http.StatusOK, map[string]any{"itens": out, "mais": mais})
}

func (s *Servidor) lerChave(w http.ResponseWriter, r *http.Request) {
	k := r.URL.Query().Get("k")
	if k == "" {
		falha(w, comandos.Uso("informe a chave"))
		return
	}
	v, err := s.cfg.Cliente.Get(r.Context(), []byte(k), s.cfg.Consistencia)
	if err != nil {
		falha(w, err)
		return
	}
	donos, _ := s.cfg.Cliente.Donos(r.Context(), []byte(k))
	responder(w, http.StatusOK, map[string]any{"item": paraJSON([]byte(k), v.Valor, v.Versao, v.Expira, false), "replicas": donos, "respostas": v.Respostas, "coordenador": v.Coordenador})
}

func (s *Servidor) gravarChave(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Chave    string `json:"chave"`
		Valor    string `json:"valor"`
		TTL      string `json:"ttl"`
		SeVersao string `json:"se_versao"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		falha(w, comandos.Uso("JSON inválido: %v", err))
		return
	}
	if strings.TrimSpace(p.Chave) == "" {
		falha(w, comandos.Uso("informe a chave"))
		return
	}
	op := cliente.OpcoesPut{Consistencia: s.cfg.Consistencia}
	if p.TTL != "" && p.TTL != "0" {
		d, err := comandos.ParseDuracao(p.TTL)
		if err != nil {
			falha(w, err)
			return
		}
		op.TTL = d
	}
	if p.SeVersao != "" {
		v, err := strconv.ParseUint(p.SeVersao, 10, 64)
		if err != nil {
			falha(w, comandos.Uso("versão inválida"))
			return
		}
		op.SeVersao = v
	}
	e, err := s.cfg.Cliente.Put(r.Context(), []byte(p.Chave), []byte(p.Valor), op)
	if err != nil {
		falha(w, err)
		return
	}
	responder(w, http.StatusOK, map[string]any{"versao": strconv.FormatUint(e.Versao, 10), "confirmacoes": e.Confirmacoes, "replicas": e.Replicas, "necessarias": e.Necessarias})
}

func (s *Servidor) apagarChave(w http.ResponseWriter, r *http.Request) {
	k := r.URL.Query().Get("k")
	if k == "" {
		falha(w, comandos.Uso("informe a chave"))
		return
	}
	e, err := s.cfg.Cliente.Del(r.Context(), []byte(k), s.cfg.Consistencia, 0)
	if err != nil {
		falha(w, err)
		return
	}
	responder(w, http.StatusOK, map[string]any{"versao": strconv.FormatUint(e.Versao, 10), "confirmacoes": e.Confirmacoes, "replicas": e.Replicas})
}

func (s *Servidor) executar(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Linha string `json:"linha"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		falha(w, comandos.Uso("JSON inválido"))
		return
	}
	tokens, err := comandos.Tokenizar(p.Linha)
	if err != nil {
		responder(w, http.StatusOK, map[string]any{"erro": err.Error(), "codigo": comandos.SaidaUso})
		return
	}
	if len(tokens) == 0 {
		responder(w, http.StatusOK, map[string]any{"saida": ""})
		return
	}
	if strings.EqualFold(tokens[0], "ajuda") || tokens[0] == "help" || tokens[0] == `\?` {
		responder(w, http.StatusOK, map[string]any{"saida": textoAjuda(tokens[1:])})
		return
	}
	cmd, resto := comandos.Buscar(tokens)
	if cmd == nil {
		responder(w, http.StatusOK, map[string]any{"erro": "comando desconhecido: " + tokens[0] + " (digite ajuda)", "codigo": comandos.SaidaUso})
		return
	}
	a, err := comandos.Analisar(cmd, resto)
	if err != nil {
		responder(w, http.StatusOK, map[string]any{"erro": err.Error(), "codigo": comandos.SaidaUso})
		return
	}
	if cmd.Perigoso && !a.Bool("confirmar") {
		responder(w, http.StatusOK, map[string]any{"erro": "operação perigosa: repita com --confirmar", "codigo": comandos.SaidaUso})
		return
	}
	ctx, cancelar := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancelar()
	res, err := comandos.Executar(ctx, s.sessao(), cmd, a)
	var b bytes.Buffer
	if res != nil {
		saida.Escrever(&b, res, saida.Tabela, saida.Estilo{})
	}
	out := map[string]any{"saida": b.String(), "codigo": comandos.CodigoSaida(err)}
	if res != nil {
		out["duracao_ms"] = float64(res.Duracao.Microseconds()) / 1000
	}
	if err != nil {
		var es *comandos.ErroSaida
		if !(errors.As(err, &es) && es.Msg == "") {
			out["erro"] = err.Error()
		}
	}
	responder(w, http.StatusOK, out)
}

func textoAjuda(args []string) string {
	if len(args) > 0 {
		if c, _ := comandos.Buscar(args); c != nil {
			return comandos.Ajuda(c, saida.Estilo{})
		}
	}
	var b strings.Builder
	grupos := comandos.PorGrupo()
	for _, g := range comandos.Ordem {
		b.WriteString(g + "\n")
		for _, c := range grupos[g] {
			fmt.Fprintf(&b, "  %-34s %s\n", c.Assinatura(), c.Resumo)
		}
		b.WriteString("\n")
	}
	b.WriteString("ajuda <comando> mostra opções e exemplos · ↑ ↓ histórico · Ctrl+L limpa")
	return b.String()
}

func (s *Servidor) listaComandos(w http.ResponseWriter, r *http.Request) {
	responder(w, http.StatusOK, map[string]any{"comandos": comandos.Nomes()})
}

func (s *Servidor) localizar(w http.ResponseWriter, r *http.Request) {
	k := r.URL.Query().Get("k")
	if k == "" {
		falha(w, comandos.Uso("informe a chave"))
		return
	}
	donos, err := s.cfg.Cliente.Donos(r.Context(), []byte(k))
	if err != nil {
		falha(w, err)
		return
	}
	responder(w, http.StatusOK, map[string]any{"chave": k, "posicao": float64(anel.Hash([]byte(k))) / float64(^uint64(0)), "replicas": donos})
}

// escritorRastreado registra se algo já foi enviado, para saber se ainda dá
// para responder um erro em JSON.
type escritorRastreado struct {
	http.ResponseWriter
	escreveu bool
}

func (e *escritorRastreado) Write(b []byte) (int, error) {
	e.escreveu = true
	return e.ResponseWriter.Write(b)
}

func formatoTransferencia(formato, nome string) (string, error) {
	formato = strings.ToLower(strings.TrimSpace(formato))
	if formato == "" {
		switch {
		case strings.HasSuffix(strings.ToLower(nome), ".csv"):
			formato = "csv"
		case strings.HasSuffix(strings.ToLower(nome), ".json"):
			formato = "json"
		default:
			formato = "ndjson"
		}
	}
	switch formato {
	case "ndjson", "json", "csv":
		return formato, nil
	}
	return "", comandos.Uso("formato desconhecido: %q (use ndjson, json ou csv)", formato)
}

func nomeArquivo(prefixo, ext string) string {
	limpo := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '-'
	}, strings.TrimRight(prefixo, ":/"))
	if limpo == "" {
		limpo = "tudo"
	}
	if len(limpo) > 40 {
		limpo = limpo[:40]
	}
	return "leme-" + limpo + "-" + time.Now().Format("20060102-150405") + "." + ext
}

func (s *Servidor) exportar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	formato, err := formatoTransferencia(q.Get("formato"), "")
	if err != nil {
		falha(w, err)
		return
	}
	tipos := map[string]string{"ndjson": "application/x-ndjson", "json": "application/json", "csv": "text/csv"}
	prefixo := q.Get("prefixo")
	ctx, cancelar := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancelar()
	w.Header().Set("Content-Type", tipos[formato]+"; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="`+nomeArquivo(prefixo, formato)+`"`)
	er := &escritorRastreado{ResponseWriter: w}
	if _, err := comandos.Exportar(ctx, s.sessao(), prefixo, formato, er); err != nil && !er.escreveu {
		w.Header().Del("Content-Disposition")
		falha(w, err)
	}
}

func (s *Servidor) importar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	formato, err := formatoTransferencia(q.Get("formato"), q.Get("nome"))
	if err != nil {
		falha(w, err)
		return
	}
	sim := func(v string) bool { return v == "1" || v == "true" || v == "sim" }
	op := comandos.OpcoesImportar{Formato: formato, SeAusente: sim(q.Get("se_ausente")), Simular: sim(q.Get("simular")), Prefixo: q.Get("prefixo")}
	ctx, cancelar := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancelar()
	rel, err := comandos.Importar(ctx, s.sessao(), r.Body, op)
	if err != nil {
		falha(w, err)
		return
	}
	responder(w, http.StatusOK, map[string]any{"relatorio": rel, "simulado": op.Simular, "formato": formato})
}

func (s *Servidor) diagnostico(w http.ResponseWriter, r *http.Request) {
	ctx, cancelar := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancelar()
	vs := comandos.Diagnosticar(ctx, s.sessao())
	cont := map[string]int{"ok": 0, "aviso": 0, "falha": 0}
	for _, v := range vs {
		cont[v.Estado]++
	}
	responder(w, http.StatusOK, map[string]any{"verificacoes": vs, "contagem": cont, "agora": time.Now().UnixMilli()})
}

func (s *Servidor) integracao(w http.ResponseWriter, r *http.Request) {
	esquema := "http"
	if r.TLS != nil {
		esquema = "https"
	}
	responder(w, http.StatusOK, map[string]any{
		"url":       esquema + "://" + r.Host,
		"protegido": s.cfg.Token != "",
		"token":     s.cfg.Token,
		"versao":    versao.Versao,
	})
}
