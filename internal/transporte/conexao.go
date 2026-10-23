package transporte

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/protocolo"
)

type Opcoes struct {
	TLS         *tls.Config
	Token       string
	TempoDiscar time.Duration
	KeepAlive   time.Duration
}

type Conexao struct {
	conn      net.Conn
	endereco  string
	escrita   sync.Mutex
	mu        sync.Mutex
	pendentes map[uint32]chan protocolo.Quadro
	proximo   atomic.Uint32
	fechada   atomic.Bool
	erro      error
	feito     chan struct{}
}

var ErrConexaoFechada = errors.New("conexão fechada")

func Discar(ctx context.Context, endereco string, op Opcoes) (*Conexao, error) {
	if op.TempoDiscar <= 0 {
		op.TempoDiscar = 3 * time.Second
	}
	if op.KeepAlive <= 0 {
		op.KeepAlive = 30 * time.Second
	}
	d := &net.Dialer{Timeout: op.TempoDiscar, KeepAlive: op.KeepAlive}
	var conn net.Conn
	var err error
	if op.TLS != nil {
		td := &tls.Dialer{NetDialer: d, Config: op.TLS}
		conn, err = td.DialContext(ctx, "tcp", endereco)
	} else {
		conn, err = d.DialContext(ctx, "tcp", endereco)
	}
	if err != nil {
		return nil, &protocolo.Erro{Codigo: protocolo.CodigoIndisponivel, Mensagem: fmt.Sprintf("não foi possível conectar a %s: %v", endereco, simplificar(err))}
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	c := &Conexao{conn: conn, endereco: endereco, pendentes: make(map[uint32]chan protocolo.Quadro), feito: make(chan struct{})}
	go c.ler()
	if op.Token != "" {
		if _, err := c.Chamar(ctx, protocolo.OpAuth, protocolo.NovoCodificador().Texto(op.Token).Bytes()); err != nil {
			c.Fechar()
			return nil, err
		}
	}
	return c, nil
}

func simplificar(err error) error {
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		return op.Err
	}
	return err
}

func (c *Conexao) Endereco() string { return c.endereco }

func (c *Conexao) Viva() bool { return !c.fechada.Load() }

func (c *Conexao) ler() {
	r := bufio.NewReaderSize(c.conn, 64<<10)
	var err error
	for {
		var q protocolo.Quadro
		q, err = protocolo.Ler(r)
		if err != nil {
			break
		}
		c.mu.Lock()
		ch, ok := c.pendentes[q.ID]
		if ok {
			delete(c.pendentes, q.ID)
		}
		c.mu.Unlock()
		if ok {
			ch <- q
		}
	}
	c.encerrar(err)
}

func (c *Conexao) encerrar(err error) {
	if c.fechada.Swap(true) {
		return
	}
	c.conn.Close()
	c.mu.Lock()
	c.erro = err
	for id, ch := range c.pendentes {
		close(ch)
		delete(c.pendentes, id)
	}
	c.mu.Unlock()
	close(c.feito)
}

func (c *Conexao) Chamar(ctx context.Context, op protocolo.Op, corpo []byte) ([]byte, error) {
	if c.fechada.Load() {
		return nil, &protocolo.Erro{Codigo: protocolo.CodigoIndisponivel, Mensagem: fmt.Sprintf("conexão com %s fechada", c.endereco)}
	}
	id := c.proximo.Add(1)
	ch := make(chan protocolo.Quadro, 1)
	c.mu.Lock()
	c.pendentes[id] = ch
	c.mu.Unlock()
	c.escrita.Lock()
	if prazo, ok := ctx.Deadline(); ok {
		c.conn.SetWriteDeadline(prazo)
	} else {
		c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	}
	err := protocolo.Escrever(c.conn, protocolo.Quadro{Op: op, ID: id, Corpo: corpo})
	c.escrita.Unlock()
	if err != nil {
		c.encerrar(err)
		return nil, &protocolo.Erro{Codigo: protocolo.CodigoIndisponivel, Mensagem: fmt.Sprintf("falha ao enviar para %s: %v", c.endereco, simplificar(err))}
	}
	select {
	case q, ok := <-ch:
		if !ok {
			return nil, &protocolo.Erro{Codigo: protocolo.CodigoIndisponivel, Mensagem: fmt.Sprintf("conexão com %s caiu durante a operação", c.endereco)}
		}
		if q.Op == protocolo.OpErro {
			return nil, protocolo.DecodificarErro(q.Corpo)
		}
		return q.Corpo, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pendentes, id)
		c.mu.Unlock()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, &protocolo.Erro{Codigo: protocolo.CodigoTempoEsgotado, Mensagem: fmt.Sprintf("%s não respondeu a tempo (%s)", c.endereco, op)}
		}
		return nil, ctx.Err()
	}
}

func (c *Conexao) Fechar() error {
	c.encerrar(ErrConexaoFechada)
	return nil
}

type Pool struct {
	op       Opcoes
	mu       sync.Mutex
	conns    map[string]*Conexao
	discando map[string]*sync.Mutex
}

func NovoPool(op Opcoes) *Pool {
	return &Pool{op: op, conns: make(map[string]*Conexao), discando: make(map[string]*sync.Mutex)}
}

func (p *Pool) Obter(ctx context.Context, endereco string) (*Conexao, error) {
	p.mu.Lock()
	if c, ok := p.conns[endereco]; ok && c.Viva() {
		p.mu.Unlock()
		return c, nil
	}
	trava, ok := p.discando[endereco]
	if !ok {
		trava = &sync.Mutex{}
		p.discando[endereco] = trava
	}
	p.mu.Unlock()
	trava.Lock()
	defer trava.Unlock()
	p.mu.Lock()
	if c, ok := p.conns[endereco]; ok && c.Viva() {
		p.mu.Unlock()
		return c, nil
	}
	p.mu.Unlock()
	c, err := Discar(ctx, endereco, p.op)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.conns[endereco] = c
	p.mu.Unlock()
	return c, nil
}

func (p *Pool) Chamar(ctx context.Context, endereco string, op protocolo.Op, corpo []byte) ([]byte, error) {
	c, err := p.Obter(ctx, endereco)
	if err != nil {
		return nil, err
	}
	return c.Chamar(ctx, op, corpo)
}

func (p *Pool) Descartar(endereco string) {
	p.mu.Lock()
	c, ok := p.conns[endereco]
	delete(p.conns, endereco)
	p.mu.Unlock()
	if ok {
		c.Fechar()
	}
}

func (p *Pool) Fechar() {
	p.mu.Lock()
	conns := p.conns
	p.conns = make(map[string]*Conexao)
	p.mu.Unlock()
	for _, c := range conns {
		c.Fechar()
	}
}

func (p *Pool) Abertas() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, c := range p.conns {
		if c.Viva() {
			n++
		}
	}
	return n
}
