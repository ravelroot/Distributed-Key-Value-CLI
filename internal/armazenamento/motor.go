package armazenamento

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Sincronizacao int

const (
	SincronizarIntervalo Sincronizacao = iota
	SincronizarSempre
	SincronizarNunca
)

func ParseSincronizacao(s string) (Sincronizacao, error) {
	switch strings.ToLower(s) {
	case "", "intervalo":
		return SincronizarIntervalo, nil
	case "sempre":
		return SincronizarSempre, nil
	case "nunca":
		return SincronizarNunca, nil
	}
	return 0, fmt.Errorf("modo de sincronização desconhecido: %q (use sempre, intervalo ou nunca)", s)
}

func (s Sincronizacao) String() string {
	switch s {
	case SincronizarSempre:
		return "sempre"
	case SincronizarNunca:
		return "nunca"
	}
	return "intervalo"
}

type Opcoes struct {
	SegmentoMax    int64
	Sincronizar    Sincronizacao
	Intervalo      time.Duration
	GracaLapide    time.Duration
	Agora          func() time.Time
	SomenteLeitura bool
}

func (o *Opcoes) normalizar() {
	if o.SegmentoMax <= 0 {
		o.SegmentoMax = 64 << 20
	}
	if o.Intervalo <= 0 {
		o.Intervalo = time.Second
	}
	if o.GracaLapide <= 0 {
		o.GracaLapide = 24 * time.Hour
	}
	if o.Agora == nil {
		o.Agora = time.Now
	}
}

type entrada struct {
	seg    uint32
	pos    int64
	tam    int64
	versao uint64
	expira int64
	lapide bool
}

type segmento struct {
	id      uint32
	arquivo *os.File
	tamanho int64
}

type Motor struct {
	dir         string
	op          Opcoes
	mu          sync.RWMutex
	indice      map[string]entrada
	segmentos   map[uint32]*segmento
	ativo       *segmento
	proximoID   uint32
	sujo        atomic.Bool
	fechado     bool
	parar       chan struct{}
	feito       chan struct{}
	compactando atomic.Bool

	gravacoes         atomic.Uint64
	leituras          atomic.Uint64
	descartados       atomic.Uint64
	compactacoes      atomic.Uint64
	ultimaCompactacao atomic.Int64
	bytesRecuperados  atomic.Int64
}

var (
	ErrFechado      = errors.New("motor de armazenamento fechado")
	ErrVersaoAntiga = errors.New("versão mais antiga que a armazenada")
	ErrChaveVazia   = errors.New("a chave não pode ser vazia")
	ErrChaveGrande  = fmt.Errorf("a chave passa de %d bytes", MaxChave)
	ErrValorGrande  = fmt.Errorf("o valor passa de %d bytes", MaxValor)
)

const extensao = ".log"

func nomeSegmento(id uint32) string { return fmt.Sprintf("%08d%s", id, extensao) }

func Abrir(dir string, op Opcoes) (*Motor, error) {
	op.normalizar()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	m := &Motor{dir: dir, op: op, indice: make(map[string]entrada), segmentos: make(map[uint32]*segmento), parar: make(chan struct{}), feito: make(chan struct{})}
	ids, err := m.listar()
	if err != nil {
		return nil, err
	}
	for i, id := range ids {
		if err := m.carregar(id, i == len(ids)-1); err != nil {
			m.fecharArquivos()
			return nil, err
		}
		if id >= m.proximoID {
			m.proximoID = id + 1
		}
	}
	if m.proximoID == 0 {
		m.proximoID = 1
	}
	if len(ids) > 0 {
		ultimo := m.segmentos[ids[len(ids)-1]]
		if ultimo.tamanho < op.SegmentoMax {
			m.ativo = ultimo
		}
	}
	if m.ativo == nil {
		if err := m.novoAtivo(); err != nil {
			m.fecharArquivos()
			return nil, err
		}
	}
	go m.laco()
	return m, nil
}

func (m *Motor) listar() ([]uint32, error) {
	itens, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, err
	}
	var ids []uint32
	for _, it := range itens {
		nome := it.Name()
		if it.IsDir() || !strings.HasSuffix(nome, extensao) {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(nome, extensao), 10, 32)
		if err != nil {
			continue
		}
		ids = append(ids, uint32(n))
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func (m *Motor) carregar(id uint32, ultimo bool) error {
	caminho := filepath.Join(m.dir, nomeSegmento(id))
	f, err := os.OpenFile(caminho, os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	tamanho := info.Size()
	var pos int64
	for pos < tamanho {
		reg, n, err := LerRegistro(f, pos)
		if err != nil {
			perdidos := tamanho - pos
			m.bytesRecuperados.Add(perdidos)
			m.descartados.Add(1)
			if ultimo {
				if err := f.Truncate(pos); err != nil {
					f.Close()
					return fmt.Errorf("truncar %s: %w", caminho, err)
				}
				tamanho = pos
			}
			break
		}
		m.indexar(string(reg.Chave), entrada{seg: id, pos: pos, tam: n, versao: reg.Versao, expira: reg.Expira, lapide: reg.Tipo == TipoLapide})
		pos += n
	}
	m.segmentos[id] = &segmento{id: id, arquivo: f, tamanho: pos}
	return nil
}

func (m *Motor) indexar(chave string, e entrada) {
	atual, ok := m.indice[chave]
	if ok && atual.versao > e.versao {
		return
	}
	m.indice[chave] = e
}

func (m *Motor) novoAtivo() error {
	id := m.proximoID
	m.proximoID++
	f, err := os.OpenFile(filepath.Join(m.dir, nomeSegmento(id)), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	s := &segmento{id: id, arquivo: f}
	m.segmentos[id] = s
	m.ativo = s
	return nil
}

func (m *Motor) laco() {
	defer close(m.feito)
	t := time.NewTicker(m.op.Intervalo)
	defer t.Stop()
	for {
		select {
		case <-m.parar:
			return
		case <-t.C:
			if m.op.Sincronizar == SincronizarIntervalo && m.sujo.Swap(false) {
				m.mu.RLock()
				if !m.fechado && m.ativo != nil {
					m.ativo.arquivo.Sync()
				}
				m.mu.RUnlock()
			}
		}
	}
}

func validar(chave, valor []byte) error {
	if len(chave) == 0 {
		return ErrChaveVazia
	}
	if len(chave) > MaxChave {
		return ErrChaveGrande
	}
	if len(valor) > MaxValor {
		return ErrValorGrande
	}
	return nil
}

func (m *Motor) Gravar(reg Registro) error {
	if err := validar(reg.Chave, reg.Valor); err != nil {
		return err
	}
	if reg.Tipo == 0 {
		reg.Tipo = TipoValor
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fechado {
		return ErrFechado
	}
	chave := string(reg.Chave)
	if atual, ok := m.indice[chave]; ok && atual.versao >= reg.Versao {
		return ErrVersaoAntiga
	}
	dados := reg.Codificar()
	if m.ativo.tamanho+int64(len(dados)) > m.op.SegmentoMax && m.ativo.tamanho > 0 {
		if err := m.ativo.arquivo.Sync(); err != nil {
			return err
		}
		if err := m.novoAtivo(); err != nil {
			return err
		}
	}
	pos := m.ativo.tamanho
	if _, err := m.ativo.arquivo.WriteAt(dados, pos); err != nil {
		return err
	}
	if m.op.Sincronizar == SincronizarSempre {
		if err := m.ativo.arquivo.Sync(); err != nil {
			return err
		}
	} else {
		m.sujo.Store(true)
	}
	m.ativo.tamanho += int64(len(dados))
	m.indice[chave] = entrada{seg: m.ativo.id, pos: pos, tam: int64(len(dados)), versao: reg.Versao, expira: reg.Expira, lapide: reg.Tipo == TipoLapide}
	m.gravacoes.Add(1)
	return nil
}

func (m *Motor) expirado(e entrada) bool {
	return e.expira > 0 && m.op.Agora().UnixMilli() >= e.expira
}

func (m *Motor) LerBruto(chave []byte) (Registro, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.fechado {
		return Registro{}, false, ErrFechado
	}
	e, ok := m.indice[string(chave)]
	if !ok {
		return Registro{}, false, nil
	}
	s := m.segmentos[e.seg]
	reg, _, err := LerRegistro(s.arquivo, e.pos)
	if err != nil {
		return Registro{}, false, err
	}
	m.leituras.Add(1)
	return reg, true, nil
}

func (m *Motor) Ler(chave []byte) (Registro, bool, error) {
	reg, ok, err := m.LerBruto(chave)
	if err != nil || !ok {
		return reg, ok, err
	}
	if reg.Tipo == TipoLapide || (reg.Expira > 0 && m.op.Agora().UnixMilli() >= reg.Expira) {
		return Registro{}, false, nil
	}
	return reg, true, nil
}

func (m *Motor) Versao(chave []byte) (uint64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.indice[string(chave)]
	return e.versao, ok
}

type FiltroVarredura struct {
	Prefixo        []byte
	Depois         []byte
	Limite         int
	IncluirLapides bool
	SoChaves       bool
}

func (m *Motor) Varrer(f FiltroVarredura) ([]Registro, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.fechado {
		return nil, false, ErrFechado
	}
	p := string(f.Prefixo)
	depois := string(f.Depois)
	chaves := make([]string, 0, 64)
	for k, e := range m.indice {
		if !strings.HasPrefix(k, p) || (depois != "" && k <= depois) {
			continue
		}
		if !f.IncluirLapides && (e.lapide || m.expirado(e)) {
			continue
		}
		chaves = append(chaves, k)
	}
	sort.Strings(chaves)
	mais := false
	if f.Limite > 0 && len(chaves) > f.Limite {
		chaves = chaves[:f.Limite]
		mais = true
	}
	saida := make([]Registro, 0, len(chaves))
	for _, k := range chaves {
		e := m.indice[k]
		if f.SoChaves {
			tipo := TipoValor
			if e.lapide {
				tipo = TipoLapide
			}
			saida = append(saida, Registro{Tipo: tipo, Versao: e.versao, Expira: e.expira, Chave: []byte(k)})
			continue
		}
		reg, _, err := LerRegistro(m.segmentos[e.seg].arquivo, e.pos)
		if err != nil {
			return nil, false, err
		}
		saida = append(saida, reg)
	}
	return saida, mais, nil
}

func (m *Motor) Percorrer(fn func(Registro) bool) error {
	regs, _, err := m.Varrer(FiltroVarredura{IncluirLapides: true})
	if err != nil {
		return err
	}
	for _, r := range regs {
		if !fn(r) {
			return nil
		}
	}
	return nil
}

type Estatisticas struct {
	Chaves            int     `json:"chaves"`
	Lapides           int     `json:"lapides"`
	Expiradas         int     `json:"expiradas"`
	BytesVivos        int64   `json:"bytes_vivos"`
	BytesDisco        int64   `json:"bytes_disco"`
	Segmentos         int     `json:"segmentos"`
	SegmentoAtivo     uint32  `json:"segmento_ativo"`
	Fragmentacao      float64 `json:"fragmentacao"`
	Gravacoes         uint64  `json:"gravacoes"`
	Leituras          uint64  `json:"leituras"`
	Compactacoes      uint64  `json:"compactacoes"`
	UltimaCompactacao int64   `json:"ultima_compactacao"`
	Descartados       uint64  `json:"registros_descartados"`
	Sincronizar       string  `json:"sincronizar"`
}

func (m *Motor) Estatisticas() Estatisticas {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st := Estatisticas{Segmentos: len(m.segmentos), Gravacoes: m.gravacoes.Load(), Leituras: m.leituras.Load(), Compactacoes: m.compactacoes.Load(), UltimaCompactacao: m.ultimaCompactacao.Load(), Descartados: m.descartados.Load(), Sincronizar: m.op.Sincronizar.String()}
	if m.ativo != nil {
		st.SegmentoAtivo = m.ativo.id
	}
	for _, s := range m.segmentos {
		st.BytesDisco += s.tamanho
	}
	for _, e := range m.indice {
		st.BytesVivos += e.tam
		switch {
		case e.lapide:
			st.Lapides++
		case m.expirado(e):
			st.Expiradas++
		default:
			st.Chaves++
		}
	}
	if st.BytesDisco > 0 {
		st.Fragmentacao = 1 - float64(st.BytesVivos)/float64(st.BytesDisco)
	}
	return st
}

type ResultadoCompactacao struct {
	SegmentosAntes  int           `json:"segmentos_antes"`
	SegmentosDepois int           `json:"segmentos_depois"`
	BytesAntes      int64         `json:"bytes_antes"`
	BytesDepois     int64         `json:"bytes_depois"`
	Removidos       int           `json:"registros_removidos"`
	Duracao         time.Duration `json:"duracao_ns"`
}

var ErrCompactando = errors.New("já existe uma compactação em andamento")

func (m *Motor) Compactar() (ResultadoCompactacao, error) {
	if !m.compactando.CompareAndSwap(false, true) {
		return ResultadoCompactacao{}, ErrCompactando
	}
	defer m.compactando.Store(false)
	inicio := time.Now()
	m.mu.Lock()
	if m.fechado {
		m.mu.Unlock()
		return ResultadoCompactacao{}, ErrFechado
	}
	if m.ativo.tamanho > 0 {
		if err := m.ativo.arquivo.Sync(); err != nil {
			m.mu.Unlock()
			return ResultadoCompactacao{}, err
		}
		if err := m.novoAtivo(); err != nil {
			m.mu.Unlock()
			return ResultadoCompactacao{}, err
		}
	}
	res := ResultadoCompactacao{SegmentosAntes: len(m.segmentos)}
	antigos := make(map[uint32]*segmento)
	for id, s := range m.segmentos {
		res.BytesAntes += s.tamanho
		if id != m.ativo.id {
			antigos[id] = s
		}
	}
	type alvo struct {
		chave string
		e     entrada
	}
	var alvos []alvo
	for k, e := range m.indice {
		if _, ok := antigos[e.seg]; ok {
			alvos = append(alvos, alvo{k, e})
		}
	}
	m.mu.Unlock()

	sort.Slice(alvos, func(i, j int) bool {
		if alvos[i].e.seg != alvos[j].e.seg {
			return alvos[i].e.seg < alvos[j].e.seg
		}
		return alvos[i].e.pos < alvos[j].e.pos
	})
	agora := m.op.Agora()
	limiteLapide := agora.Add(-m.op.GracaLapide).UnixMilli()
	tmp := filepath.Join(m.dir, "compactacao.tmp")
	saida, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return res, err
	}
	novos := make(map[string]entrada, len(alvos))
	remover := make(map[string]entrada)
	var pos int64
	var buf bytes.Buffer
	for _, a := range alvos {
		if a.e.lapide && MilissegundosDaVersao(a.e.versao) < limiteLapide {
			remover[a.chave] = a.e
			res.Removidos++
			continue
		}
		if !a.e.lapide && a.e.expira > 0 && agora.UnixMilli() >= a.e.expira {
			remover[a.chave] = a.e
			res.Removidos++
			continue
		}
		reg, n, err := LerRegistro(antigos[a.e.seg].arquivo, a.e.pos)
		if err != nil {
			saida.Close()
			os.Remove(tmp)
			return res, err
		}
		buf.Write(reg.Codificar())
		novos[a.chave] = entrada{pos: pos, tam: n, versao: a.e.versao, expira: a.e.expira, lapide: a.e.lapide}
		pos += n
		if buf.Len() >= 1<<20 {
			if _, err := saida.Write(buf.Bytes()); err != nil {
				saida.Close()
				os.Remove(tmp)
				return res, err
			}
			buf.Reset()
		}
	}
	if _, err := saida.Write(buf.Bytes()); err != nil {
		saida.Close()
		os.Remove(tmp)
		return res, err
	}
	if err := saida.Sync(); err != nil {
		saida.Close()
		os.Remove(tmp)
		return res, err
	}
	saida.Close()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fechado {
		os.Remove(tmp)
		return res, ErrFechado
	}
	id := m.proximoID
	m.proximoID++
	final := filepath.Join(m.dir, nomeSegmento(id))
	if pos > 0 {
		if err := os.Rename(tmp, final); err != nil {
			os.Remove(tmp)
			return res, err
		}
		f, err := os.OpenFile(final, os.O_RDWR, 0o644)
		if err != nil {
			return res, err
		}
		m.segmentos[id] = &segmento{id: id, arquivo: f, tamanho: pos}
	} else {
		os.Remove(tmp)
	}
	for k, ne := range novos {
		atual, ok := m.indice[k]
		if !ok {
			continue
		}
		if _, eraAntigo := antigos[atual.seg]; !eraAntigo || atual.versao != ne.versao {
			continue
		}
		ne.seg = id
		m.indice[k] = ne
	}
	for k, e := range remover {
		if atual, ok := m.indice[k]; ok && atual.seg == e.seg && atual.pos == e.pos {
			delete(m.indice, k)
		}
	}
	for idAntigo, s := range antigos {
		s.arquivo.Close()
		os.Remove(filepath.Join(m.dir, nomeSegmento(idAntigo)))
		delete(m.segmentos, idAntigo)
	}
	res.SegmentosDepois = len(m.segmentos)
	for _, s := range m.segmentos {
		res.BytesDepois += s.tamanho
	}
	res.Duracao = time.Since(inicio)
	m.compactacoes.Add(1)
	m.ultimaCompactacao.Store(time.Now().UnixMilli())
	return res, nil
}

func (m *Motor) Sincronizar() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.fechado {
		return ErrFechado
	}
	return m.ativo.arquivo.Sync()
}

func (m *Motor) fecharArquivos() {
	for _, s := range m.segmentos {
		s.arquivo.Close()
	}
}

func (m *Motor) Fechar() error {
	m.mu.Lock()
	if m.fechado {
		m.mu.Unlock()
		return nil
	}
	m.fechado = true
	var err error
	if m.ativo != nil {
		err = m.ativo.arquivo.Sync()
	}
	m.fecharArquivos()
	m.mu.Unlock()
	close(m.parar)
	<-m.feito
	return err
}

func (m *Motor) Diretorio() string { return m.dir }

func MilissegundosDaVersao(v uint64) int64 { return int64(v >> 16) }
