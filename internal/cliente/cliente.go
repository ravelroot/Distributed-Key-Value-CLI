package cliente

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/anel"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/no"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/protocolo"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/transporte"
)

type Config struct {
	Nos          []string
	Token        string
	TLS          *tls.Config
	TempoLimite  time.Duration
	Tentativas   int
	Consistencia protocolo.Consistencia
	Atualizar    time.Duration
}

type Cliente struct {
	cfg        Config
	pool       *transporte.Pool
	mu         sync.RWMutex
	topologia  no.TopologiaCompleta
	anel       *anel.Anel
	atualizado time.Time
	ruins      map[string]time.Time
}

func Novo(cfg Config) (*Cliente, error) {
	var nos []string
	for _, n := range cfg.Nos {
		for _, p := range strings.Split(n, ",") {
			if p = strings.TrimSpace(p); p != "" {
				nos = append(nos, p)
			}
		}
	}
	if len(nos) == 0 {
		return nil, errors.New("informe ao menos um nó do cluster (--nos host:porta)")
	}
	cfg.Nos = nos
	if cfg.TempoLimite <= 0 {
		cfg.TempoLimite = 5 * time.Second
	}
	if cfg.Tentativas <= 0 {
		cfg.Tentativas = 3
	}
	if cfg.Atualizar <= 0 {
		cfg.Atualizar = 10 * time.Second
	}
	return &Cliente{cfg: cfg, ruins: map[string]time.Time{}, pool: transporte.NovoPool(transporte.Opcoes{TLS: cfg.TLS, Token: cfg.Token, TempoDiscar: cfg.TempoLimite})}, nil
}

func (c *Cliente) Config() Config { return c.cfg }

func (c *Cliente) Fechar() { c.pool.Fechar() }

func (c *Cliente) contexto(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, c.cfg.TempoLimite)
}

func (c *Cliente) Conectar(ctx context.Context) error {
	_, err := c.Atualizar(ctx)
	return err
}

func (c *Cliente) Atualizar(ctx context.Context) (no.TopologiaCompleta, error) {
	ctx, cancelar := c.contexto(ctx)
	defer cancelar()
	candidatos := append([]string{}, c.cfg.Nos...)
	c.mu.RLock()
	for _, m := range c.topologia.Membros {
		if m.Estado == "vivo" {
			candidatos = append(candidatos, m.Endereco)
		}
	}
	c.mu.RUnlock()
	var ultimo error
	vistos := map[string]bool{}
	for _, end := range candidatos {
		if vistos[end] {
			continue
		}
		vistos[end] = true
		resp, err := c.pool.Chamar(ctx, end, protocolo.OpMembros, nil)
		if err != nil {
			ultimo = err
			if protocolo.CodigoDe(err) == protocolo.CodigoNaoAutorizado {
				return no.TopologiaCompleta{}, err
			}
			continue
		}
		var t no.TopologiaCompleta
		if err := json.Unmarshal(resp, &t); err != nil {
			ultimo = err
			continue
		}
		a := anel.Novo(t.VNodes)
		var ms []anel.Membro
		for _, m := range t.Membros {
			if m.Estado != "saiu" {
				ms = append(ms, anel.Membro{ID: m.ID, Endereco: m.Endereco})
			}
		}
		a.Definir(ms)
		c.mu.Lock()
		c.topologia = t
		c.anel = a
		c.atualizado = time.Now()
		c.mu.Unlock()
		return t, nil
	}
	if ultimo == nil {
		ultimo = errors.New("nenhum nó informado")
	}
	return no.TopologiaCompleta{}, ultimo
}

func (c *Cliente) Topologia(ctx context.Context) (no.TopologiaCompleta, error) {
	return c.Atualizar(ctx)
}

func (c *Cliente) garantir(ctx context.Context) error {
	c.mu.RLock()
	pronto := c.anel != nil && time.Since(c.atualizado) < c.cfg.Atualizar
	c.mu.RUnlock()
	if pronto {
		return nil
	}
	_, err := c.Atualizar(ctx)
	if err != nil {
		c.mu.RLock()
		temAnel := c.anel != nil
		c.mu.RUnlock()
		if temAnel && protocolo.CodigoDe(err) != protocolo.CodigoNaoAutorizado {
			return nil
		}
	}
	return err
}

func (c *Cliente) Membros() []protocolo.MembroInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]protocolo.MembroInfo{}, c.topologia.Membros...)
}

func (c *Cliente) Donos(ctx context.Context, chave []byte) ([]protocolo.MembroInfo, error) {
	if err := c.garantir(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	estado := map[string]protocolo.MembroInfo{}
	for _, m := range c.topologia.Membros {
		estado[m.ID] = m
	}
	var out []protocolo.MembroInfo
	for _, d := range c.anel.Donos(chave, c.topologia.Replicas) {
		out = append(out, estado[d.ID])
	}
	return out, nil
}

func (c *Cliente) rota(chave []byte) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	estado := map[string]string{}
	for _, m := range c.topologia.Membros {
		estado[m.Endereco] = m.Estado
	}
	var vivos, outros []string
	vistos := map[string]bool{}
	add := func(e string) {
		if vistos[e] {
			return
		}
		vistos[e] = true
		if estado[e] == "vivo" || estado[e] == "" {
			vivos = append(vivos, e)
		} else if estado[e] != "saiu" {
			outros = append(outros, e)
		}
	}
	if c.anel != nil && chave != nil {
		for _, d := range c.anel.Donos(chave, c.topologia.Replicas) {
			add(d.Endereco)
		}
	}
	var resto []string
	for _, m := range c.topologia.Membros {
		resto = append(resto, m.Endereco)
	}
	rand.Shuffle(len(resto), func(i, j int) { resto[i], resto[j] = resto[j], resto[i] })
	for _, e := range resto {
		add(e)
	}
	for _, e := range c.cfg.Nos {
		add(e)
	}
	todos := append(vivos, outros...)
	var bons, ruins []string
	for _, e := range todos {
		if t, ok := c.ruins[e]; ok && time.Since(t) < 30*time.Second {
			ruins = append(ruins, e)
		} else {
			bons = append(bons, e)
		}
	}
	return append(bons, ruins...)
}

func (c *Cliente) marcarRuim(e string) {
	c.mu.Lock()
	c.ruins[e] = time.Now()
	c.mu.Unlock()
}

func repetivel(err error) bool {
	switch protocolo.CodigoDe(err) {
	case protocolo.CodigoIndisponivel, protocolo.CodigoTempoEsgotado:
		return true
	}
	return false
}

func (c *Cliente) chamar(ctx context.Context, chave []byte, op protocolo.Op, corpo []byte, podeRepetir bool) ([]byte, string, error) {
	if err := c.garantir(ctx); err != nil {
		return nil, "", err
	}
	ctx, cancelar := c.contexto(ctx)
	defer cancelar()
	rota := c.rota(chave)
	var ultimo error
	tentativas := c.cfg.Tentativas
	falhasDiscagem := 0
	for i := 0; i < tentativas+falhasDiscagem && i < len(rota) && i < 16; i++ {
		end := rota[i]
		_, errDiscar := c.pool.Obter(ctx, end)
		if errDiscar != nil {
			ultimo = errDiscar
			c.marcarRuim(end)
			if protocolo.CodigoDe(errDiscar) == protocolo.CodigoNaoAutorizado {
				return nil, end, errDiscar
			}
			falhasDiscagem++
			continue
		}
		resp, err := c.pool.Chamar(ctx, end, op, corpo)
		if err == nil {
			return resp, end, nil
		}
		ultimo = err
		if !repetivel(err) || ctx.Err() != nil {
			return nil, end, err
		}
		c.pool.Descartar(end)
		if !podeRepetir && protocolo.CodigoDe(err) == protocolo.CodigoTempoEsgotado {
			return nil, end, err
		}
		espera := time.Duration(25*(1<<i)) * time.Millisecond
		espera += time.Duration(rand.Int64N(int64(espera)/2 + 1))
		select {
		case <-time.After(espera):
		case <-ctx.Done():
			return nil, end, err
		}
	}
	go c.Atualizar(context.Background())
	return nil, "", ultimo
}

type Valor struct {
	Chave       []byte
	Valor       []byte
	Versao      uint64
	Expira      int64
	Respostas   int
	Coordenador string
}

func (v Valor) TTL() time.Duration {
	if v.Expira == 0 {
		return 0
	}
	d := time.Until(time.UnixMilli(v.Expira))
	if d < 0 {
		return 0
	}
	return d
}

func (c *Cliente) consistencia(x protocolo.Consistencia) protocolo.Consistencia {
	if x == protocolo.ConsistenciaPadrao {
		return c.cfg.Consistencia
	}
	return x
}

func (c *Cliente) Get(ctx context.Context, chave []byte, cons protocolo.Consistencia) (Valor, error) {
	corpo := protocolo.PedidoChave{Chave: chave, Consistencia: c.consistencia(cons)}.Codificar()
	resp, end, err := c.chamar(ctx, chave, protocolo.OpGet, corpo, true)
	if err != nil {
		return Valor{}, err
	}
	r, err := protocolo.DecodificarValor(resp)
	if err != nil {
		return Valor{}, err
	}
	return Valor{Chave: chave, Valor: r.Item.Valor, Versao: r.Item.Versao, Expira: r.Item.Expira, Respostas: r.Respostas, Coordenador: end}, nil
}

type OpcoesPut struct {
	TTL          time.Duration
	Consistencia protocolo.Consistencia
	SeVersao     uint64
	SeAusente    bool
}

type Escrita struct {
	protocolo.RespostaEscrita
	Coordenador string
}

func (c *Cliente) Put(ctx context.Context, chave, valor []byte, op OpcoesPut) (Escrita, error) {
	corpo := protocolo.PedidoPut{Chave: chave, Valor: valor, TTL: op.TTL.Milliseconds(), Consistencia: c.consistencia(op.Consistencia), SeVersao: op.SeVersao, SeAusente: op.SeAusente}.Codificar()
	resp, end, err := c.chamar(ctx, chave, protocolo.OpPut, corpo, op.SeVersao == 0 && !op.SeAusente)
	if err != nil {
		return Escrita{Coordenador: end}, err
	}
	r, err := protocolo.DecodificarEscrita(resp)
	return Escrita{RespostaEscrita: r, Coordenador: end}, err
}

func (c *Cliente) Del(ctx context.Context, chave []byte, cons protocolo.Consistencia, seVersao uint64) (Escrita, error) {
	corpo := protocolo.PedidoChave{Chave: chave, Consistencia: c.consistencia(cons), SeVersao: seVersao}.Codificar()
	resp, end, err := c.chamar(ctx, chave, protocolo.OpDel, corpo, seVersao == 0)
	if err != nil {
		return Escrita{Coordenador: end}, err
	}
	r, err := protocolo.DecodificarEscrita(resp)
	return Escrita{RespostaEscrita: r, Coordenador: end}, err
}

func (c *Cliente) Expirar(ctx context.Context, chave []byte, ttl time.Duration, cons protocolo.Consistencia) (Escrita, error) {
	corpo := protocolo.PedidoChave{Chave: chave, Consistencia: c.consistencia(cons), TTL: ttl.Milliseconds()}.Codificar()
	resp, end, err := c.chamar(ctx, chave, protocolo.OpExpirar, corpo, true)
	if err != nil {
		return Escrita{Coordenador: end}, err
	}
	r, err := protocolo.DecodificarEscrita(resp)
	return Escrita{RespostaEscrita: r, Coordenador: end}, err
}

func (c *Cliente) TTL(ctx context.Context, chave []byte, cons protocolo.Consistencia) (Valor, error) {
	corpo := protocolo.PedidoChave{Chave: chave, Consistencia: c.consistencia(cons)}.Codificar()
	resp, end, err := c.chamar(ctx, chave, protocolo.OpTTL, corpo, true)
	if err != nil {
		return Valor{}, err
	}
	r, err := protocolo.DecodificarValor(resp)
	if err != nil {
		return Valor{}, err
	}
	return Valor{Chave: chave, Versao: r.Item.Versao, Expira: r.Item.Expira, Respostas: r.Respostas, Coordenador: end}, nil
}

type OpcoesScan struct {
	Prefixo  string
	Depois   string
	Limite   int
	SoChaves bool
}

func (c *Cliente) Scan(ctx context.Context, op OpcoesScan) ([]protocolo.Item, bool, error) {
	corpo := protocolo.PedidoScan{Prefixo: []byte(op.Prefixo), Depois: []byte(op.Depois), Limite: op.Limite, SoChaves: op.SoChaves}.Codificar()
	resp, _, err := c.chamar(ctx, nil, protocolo.OpScan, corpo, true)
	if err != nil {
		return nil, false, err
	}
	r, err := protocolo.DecodificarRespostaScan(resp)
	return r.Itens, r.Mais, err
}

func (c *Cliente) Contar(ctx context.Context, prefixo string) (int, error) {
	total := 0
	depois := ""
	for {
		itens, mais, err := c.Scan(ctx, OpcoesScan{Prefixo: prefixo, Depois: depois, Limite: 5000, SoChaves: true})
		if err != nil {
			return total, err
		}
		total += len(itens)
		if !mais || len(itens) == 0 {
			return total, nil
		}
		depois = string(itens[len(itens)-1].Chave)
	}
}

func (c *Cliente) resolver(ctx context.Context, alvo string) (string, error) {
	if err := c.garantir(ctx); err != nil {
		return "", err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, m := range c.topologia.Membros {
		if m.ID == alvo || m.Endereco == alvo {
			return m.Endereco, nil
		}
	}
	if strings.Contains(alvo, ":") {
		return alvo, nil
	}
	return "", protocolo.NovoErro(protocolo.CodigoNaoEncontrado, "nó desconhecido: %s", alvo)
}

func (c *Cliente) Alvos(ctx context.Context, alvo string) ([]string, error) {
	if alvo == "" || alvo == "todos" || alvo == "all" {
		if err := c.garantir(ctx); err != nil {
			return nil, err
		}
		var out []string
		for _, m := range c.Membros() {
			if m.Estado != "saiu" {
				out = append(out, m.Endereco)
			}
		}
		sort.Strings(out)
		return out, nil
	}
	end, err := c.resolver(ctx, alvo)
	if err != nil {
		return nil, err
	}
	return []string{end}, nil
}

type Ping struct {
	Endereco string        `json:"endereco"`
	ID       string        `json:"id"`
	Latencia time.Duration `json:"latencia_ns"`
	Erro     string        `json:"erro,omitempty"`
}

func (c *Cliente) Ping(ctx context.Context, endereco string) Ping {
	ctx, cancelar := c.contexto(ctx)
	defer cancelar()
	inicio := time.Now()
	resp, err := c.pool.Chamar(ctx, endereco, protocolo.OpPing, nil)
	p := Ping{Endereco: endereco, Latencia: time.Since(inicio)}
	if err != nil {
		p.Erro = err.Error()
		c.pool.Descartar(endereco)
		return p
	}
	p.ID = protocolo.NovoDecodificador(resp).Texto()
	return p
}

func (c *Cliente) Estatisticas(ctx context.Context, endereco string) (no.Estatisticas, error) {
	ctx, cancelar := c.contexto(ctx)
	defer cancelar()
	resp, err := c.pool.Chamar(ctx, endereco, protocolo.OpStats, nil)
	if err != nil {
		return no.Estatisticas{}, err
	}
	var st no.Estatisticas
	err = json.Unmarshal(resp, &st)
	return st, err
}

func (c *Cliente) TodasEstatisticas(ctx context.Context) ([]no.Estatisticas, []error) {
	alvos, err := c.Alvos(ctx, "todos")
	if err != nil {
		return nil, []error{err}
	}
	out := make([]no.Estatisticas, len(alvos))
	erros := make([]error, len(alvos))
	var wg sync.WaitGroup
	for i, a := range alvos {
		wg.Add(1)
		go func(i int, a string) {
			defer wg.Done()
			out[i], erros[i] = c.Estatisticas(ctx, a)
		}(i, a)
	}
	wg.Wait()
	var oks []no.Estatisticas
	var errs []error
	for i := range alvos {
		if erros[i] != nil {
			errs = append(errs, fmt.Errorf("%s: %w", alvos[i], erros[i]))
			continue
		}
		oks = append(oks, out[i])
	}
	return oks, errs
}

func (c *Cliente) Compactar(ctx context.Context, endereco string) (map[string]any, error) {
	ctx, cancelar := context.WithTimeout(ctx, 10*time.Minute)
	defer cancelar()
	resp, err := c.pool.Chamar(ctx, endereco, protocolo.OpCompactar, nil)
	if err != nil {
		return nil, err
	}
	var r map[string]any
	err = json.Unmarshal(resp, &r)
	return r, err
}

func (c *Cliente) Sair(ctx context.Context, endereco string) error {
	ctx, cancelar := c.contexto(ctx)
	defer cancelar()
	_, err := c.pool.Chamar(ctx, endereco, protocolo.OpSair, nil)
	return err
}
