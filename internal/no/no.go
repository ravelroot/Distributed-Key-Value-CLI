package no

import (
	"bufio"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/anel"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/armazenamento"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/protocolo"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/transporte"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/versao"
)

type Config struct {
	ID              string
	Cluster         string
	Endereco        string
	Anunciar        string
	Dados           string
	Sementes        []string
	Replicas        int
	VNodes          int
	Token           string
	TLS             transporte.ArquivosTLS
	Metricas        string
	IntervaloGossip time.Duration
	TempoSuspeito   time.Duration
	TempoMorto      time.Duration
	TempoOperacao   time.Duration
	MaxConexoes     int
	MaxDicas        int
	Armazenamento   armazenamento.Opcoes
	Log             *slog.Logger
}

func (c *Config) normalizar() error {
	if c.Endereco == "" {
		c.Endereco = "127.0.0.1:7400"
	}
	if c.Dados == "" {
		c.Dados = "dados"
	}
	if c.Cluster == "" {
		c.Cluster = "leme"
	}
	if c.Replicas <= 0 {
		c.Replicas = 3
	}
	if c.VNodes <= 0 {
		c.VNodes = 128
	}
	if c.IntervaloGossip <= 0 {
		c.IntervaloGossip = time.Second
	}
	if c.TempoSuspeito <= 0 {
		c.TempoSuspeito = 3 * c.IntervaloGossip
	}
	if c.TempoMorto <= 0 {
		c.TempoMorto = 10 * c.IntervaloGossip
	}
	if c.TempoOperacao <= 0 {
		c.TempoOperacao = 3 * time.Second
	}
	if c.MaxConexoes <= 0 {
		c.MaxConexoes = 4096
	}
	if c.MaxDicas <= 0 {
		c.MaxDicas = 100000
	}
	if c.Log == nil {
		c.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return nil
}

type No struct {
	cfg     Config
	log     *slog.Logger
	motor   *armazenamento.Motor
	anel    *anel.Anel
	relogio *Relogio
	pool    *transporte.Pool
	tlsSrv  *tls.Config
	ouvinte net.Listener
	http    *http.Server
	inicio  time.Time
	geracao uint64

	mu         sync.RWMutex
	membros    map[string]*membro
	assinatura string

	dicas    *dicas
	metricas *metricas

	conns    sync.Map
	nConns   atomic.Int64
	handlers sync.WaitGroup
	laco     sync.WaitGroup
	parar    chan struct{}
	parado   atomic.Bool
	saindo   atomic.Bool
	redist   sync.Mutex
}

func Novo(cfg Config) (*No, error) {
	if err := cfg.normalizar(); err != nil {
		return nil, err
	}
	n := &No{cfg: cfg, log: cfg.Log, anel: anel.Novo(cfg.VNodes), relogio: NovoRelogio(cfg.Armazenamento.Agora), membros: make(map[string]*membro), parar: make(chan struct{}), metricas: novasMetricas(), dicas: novasDicas(cfg.MaxDicas)}
	srv, err := cfg.TLS.Servidor()
	if err != nil {
		return nil, err
	}
	n.tlsSrv = srv
	var cli *tls.Config
	if srv != nil {
		cli, err = cfg.TLS.Cliente()
		if err != nil {
			return nil, err
		}
		if cli == nil {
			cli = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		cli.Certificates = srv.Certificates
		if cfg.TLS.CA == "" {
			cli.InsecureSkipVerify = true
		}
	}
	n.pool = transporte.NovoPool(transporte.Opcoes{TLS: cli, Token: cfg.Token, TempoDiscar: cfg.TempoOperacao})
	return n, nil
}

func (n *No) Iniciar(ctx context.Context) error {
	ln, err := net.Listen("tcp", n.cfg.Endereco)
	if err != nil {
		return fmt.Errorf("escutar em %s: %w", n.cfg.Endereco, err)
	}
	if n.cfg.Anunciar == "" {
		n.cfg.Anunciar = anunciavel(ln.Addr().String())
	}
	if n.cfg.ID == "" {
		host, _ := os.Hostname()
		n.cfg.ID = gerarID(host, n.cfg.Anunciar)
	}
	dir := filepath.Join(n.cfg.Dados, n.cfg.ID)
	motor, err := armazenamento.Abrir(dir, n.cfg.Armazenamento)
	if err != nil {
		ln.Close()
		return fmt.Errorf("abrir armazenamento em %s: %w", dir, err)
	}
	n.motor = motor
	if n.tlsSrv != nil {
		ln = tls.NewListener(ln, n.tlsSrv)
	}
	n.ouvinte = ln
	n.inicio = time.Now()
	n.geracao = uint64(n.inicio.UnixNano())
	n.mu.Lock()
	n.membros[n.cfg.ID] = &membro{info: protocolo.MembroInfo{ID: n.cfg.ID, Endereco: n.cfg.Anunciar, Estado: EstadoVivo, Geracao: n.geracao, Desde: n.inicio.UnixMilli(), UltimoContato: n.inicio.UnixMilli(), Versao: versao.Versao}}
	n.mu.Unlock()
	n.reconstruirAnel()
	st := motor.Estatisticas()
	n.log.Info("nó iniciado", "id", n.cfg.ID, "endereco", n.cfg.Anunciar, "cluster", n.cfg.Cluster, "replicas", n.cfg.Replicas, "chaves", st.Chaves, "segmentos", st.Segmentos, "tls", n.tlsSrv != nil)
	if st.Descartados > 0 {
		n.log.Warn("registros corrompidos descartados na recuperação", "quantidade", st.Descartados)
	}
	n.laco.Add(1)
	go n.aceitar()
	if n.cfg.Metricas != "" {
		if err := n.iniciarHTTP(); err != nil {
			n.Parar(context.Background())
			return err
		}
	}
	if len(n.cfg.Sementes) > 0 {
		n.entrarNoCluster(ctx)
	}
	n.laco.Add(2)
	go n.lacoGossip()
	go n.lacoDicas()
	return nil
}

func anunciavel(endereco string) string {
	host, porta, err := net.SplitHostPort(endereco)
	if err != nil {
		return endereco
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, porta)
}

func gerarID(host, endereco string) string {
	host = strings.ToLower(strings.Split(host, ".")[0])
	if host == "" {
		host = "no"
	}
	_, porta, _ := net.SplitHostPort(endereco)
	return fmt.Sprintf("%s-%s", host, porta)
}

func (n *No) ID() string { return n.cfg.ID }

func (n *No) Endereco() string { return n.cfg.Anunciar }

func (n *No) Motor() *armazenamento.Motor { return n.motor }

func (n *No) aceitar() {
	defer n.laco.Done()
	var atraso time.Duration
	for {
		conn, err := n.ouvinte.Accept()
		if err != nil {
			if n.parado.Load() || errors.Is(err, net.ErrClosed) {
				return
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if atraso == 0 {
					atraso = 5 * time.Millisecond
				} else if atraso < time.Second {
					atraso *= 2
				}
				time.Sleep(atraso)
				continue
			}
			n.log.Error("falha ao aceitar conexão", "erro", err)
			return
		}
		atraso = 0
		if n.nConns.Load() >= int64(n.cfg.MaxConexoes) {
			n.metricas.recusadas.Add(1)
			conn.Close()
			continue
		}
		n.nConns.Add(1)
		n.conns.Store(conn, struct{}{})
		n.handlers.Add(1)
		go n.atender(conn)
	}
}

type sessao struct {
	conn        net.Conn
	escrita     sync.Mutex
	autenticada bool
	pendentes   sync.WaitGroup
}

func (n *No) atender(conn net.Conn) {
	defer func() {
		n.conns.Delete(conn)
		n.nConns.Add(-1)
		conn.Close()
		n.handlers.Done()
	}()
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
		tc.SetKeepAlive(true)
	}
	s := &sessao{conn: conn, autenticada: n.cfg.Token == ""}
	r := bufio.NewReaderSize(conn, 64<<10)
	sem := make(chan struct{}, 256)
	for {
		q, err := protocolo.Ler(r)
		if err != nil {
			if errors.Is(err, protocolo.ErrQuadroGrande) || errors.Is(err, protocolo.ErrVersao) {
				s.responderErro(0, protocolo.NovoErro(protocolo.CodigoRequisicaoInvalida, "%v", err))
			}
			break
		}
		if q.Op == protocolo.OpAuth {
			token := protocolo.NovoDecodificador(q.Corpo).Texto()
			if n.cfg.Token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(n.cfg.Token)) == 1 {
				s.autenticada = true
				s.responder(q.ID, nil)
			} else {
				n.metricas.falhasAuth.Add(1)
				time.Sleep(200 * time.Millisecond)
				s.responderErro(q.ID, protocolo.NovoErro(protocolo.CodigoNaoAutorizado, "token inválido"))
				break
			}
			continue
		}
		if !s.autenticada && q.Op != protocolo.OpPing {
			s.responderErro(q.ID, protocolo.NovoErro(protocolo.CodigoNaoAutorizado, "autenticação obrigatória: informe o token"))
			break
		}
		sem <- struct{}{}
		s.pendentes.Add(1)
		go func(q protocolo.Quadro) {
			defer func() {
				if rec := recover(); rec != nil {
					n.log.Error("pânico ao processar requisição", "op", q.Op.String(), "erro", rec)
					s.responderErro(q.ID, protocolo.NovoErro(protocolo.CodigoInterno, "erro interno no nó %s", n.cfg.ID))
				}
				<-sem
				s.pendentes.Done()
			}()
			inicio := time.Now()
			ctx, cancelar := context.WithTimeout(context.Background(), n.cfg.TempoOperacao)
			corpo, err := n.processar(ctx, q)
			cancelar()
			n.metricas.registrar(q.Op, time.Since(inicio), err)
			if err != nil {
				var pe *protocolo.Erro
				if !errors.As(err, &pe) {
					pe = protocolo.NovoErro(protocolo.CodigoInterno, "%v", err)
				}
				s.responderErro(q.ID, pe)
				return
			}
			s.responder(q.ID, corpo)
		}(q)
	}
	s.pendentes.Wait()
}

func (s *sessao) responder(id uint32, corpo []byte) {
	s.escrita.Lock()
	defer s.escrita.Unlock()
	s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	protocolo.Escrever(s.conn, protocolo.Quadro{Op: protocolo.OpResposta, ID: id, Corpo: corpo})
}

func (s *sessao) responderErro(id uint32, e *protocolo.Erro) {
	s.escrita.Lock()
	defer s.escrita.Unlock()
	s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	protocolo.Escrever(s.conn, protocolo.Quadro{Op: protocolo.OpErro, ID: id, Corpo: protocolo.CodificarErro(e)})
}

func (n *No) Parar(ctx context.Context) error {
	if n.parado.Swap(true) {
		return nil
	}
	close(n.parar)
	if n.ouvinte != nil {
		n.ouvinte.Close()
	}
	if n.http != nil {
		n.http.Shutdown(ctx)
	}
	feito := make(chan struct{})
	go func() {
		n.handlers.Wait()
		close(feito)
	}()
	n.conns.Range(func(k, _ any) bool {
		k.(net.Conn).SetReadDeadline(time.Now())
		return true
	})
	select {
	case <-feito:
	case <-ctx.Done():
		n.conns.Range(func(k, _ any) bool {
			k.(net.Conn).Close()
			return true
		})
		<-feito
	}
	n.laco.Wait()
	n.pool.Fechar()
	var err error
	if n.motor != nil {
		err = n.motor.Fechar()
	}
	n.log.Info("nó parado", "id", n.cfg.ID)
	return err
}

func (n *No) Derrubar() {
	if n.parado.Swap(true) {
		return
	}
	close(n.parar)
	n.ouvinte.Close()
	if n.http != nil {
		n.http.Close()
	}
	n.conns.Range(func(k, _ any) bool {
		k.(net.Conn).Close()
		return true
	})
	n.handlers.Wait()
	n.laco.Wait()
	n.pool.Fechar()
	n.motor.Fechar()
}
