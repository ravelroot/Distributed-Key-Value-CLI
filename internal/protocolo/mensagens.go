package protocolo

import (
	"fmt"
	"strings"
)

type Consistencia byte

const (
	ConsistenciaPadrao Consistencia = iota
	ConsistenciaUm
	ConsistenciaQuorum
	ConsistenciaTodos
)

func (c Consistencia) String() string {
	switch c {
	case ConsistenciaUm:
		return "um"
	case ConsistenciaQuorum:
		return "quorum"
	case ConsistenciaTodos:
		return "todos"
	}
	return "padrao"
}

func ParseConsistencia(s string) (Consistencia, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "padrao", "padrão":
		return ConsistenciaPadrao, nil
	case "um", "one", "1":
		return ConsistenciaUm, nil
	case "quorum", "quórum":
		return ConsistenciaQuorum, nil
	case "todos", "all":
		return ConsistenciaTodos, nil
	}
	return 0, fmt.Errorf("consistência desconhecida: %q (use um, quorum ou todos)", s)
}

func (c Consistencia) Necessarias(n int) int {
	switch c {
	case ConsistenciaUm:
		return 1
	case ConsistenciaTodos:
		return n
	}
	return n/2 + 1
}

type Item struct {
	Chave  []byte `json:"chave"`
	Valor  []byte `json:"valor,omitempty"`
	Versao uint64 `json:"versao"`
	Expira int64  `json:"expira,omitempty"`
	Lapide bool   `json:"lapide,omitempty"`
}

func (c *Codificador) Item(i Item) *Codificador {
	return c.Dados(i.Chave).Dados(i.Valor).U64(i.Versao).Varint(i.Expira).Bool(i.Lapide)
}

func (d *Decodificador) Item() Item {
	return Item{Chave: d.Dados(), Valor: d.Dados(), Versao: d.U64(), Expira: d.Varint(), Lapide: d.Bool()}
}

type PedidoPut struct {
	Chave        []byte
	Valor        []byte
	TTL          int64
	Consistencia Consistencia
	SeVersao     uint64
	SeAusente    bool
}

func (p PedidoPut) Codificar() []byte {
	return NovoCodificador().Dados(p.Chave).Dados(p.Valor).Varint(p.TTL).Byte(byte(p.Consistencia)).U64(p.SeVersao).Bool(p.SeAusente).Bytes()
}

func DecodificarPut(b []byte) (PedidoPut, error) {
	d := NovoDecodificador(b)
	p := PedidoPut{Chave: d.Dados(), Valor: d.Dados(), TTL: d.Varint(), Consistencia: Consistencia(d.Byte()), SeVersao: d.U64(), SeAusente: d.Bool()}
	return p, d.Err()
}

type PedidoChave struct {
	Chave        []byte
	Consistencia Consistencia
	SeVersao     uint64
	TTL          int64
}

func (p PedidoChave) Codificar() []byte {
	return NovoCodificador().Dados(p.Chave).Byte(byte(p.Consistencia)).U64(p.SeVersao).Varint(p.TTL).Bytes()
}

func DecodificarChave(b []byte) (PedidoChave, error) {
	d := NovoDecodificador(b)
	p := PedidoChave{Chave: d.Dados(), Consistencia: Consistencia(d.Byte()), SeVersao: d.U64(), TTL: d.Varint()}
	return p, d.Err()
}

type RespostaValor struct {
	Encontrado bool
	Item       Item
	Respostas  int
}

func (r RespostaValor) Codificar() []byte {
	return NovoCodificador().Bool(r.Encontrado).Item(r.Item).Uvarint(uint64(r.Respostas)).Bytes()
}

func DecodificarValor(b []byte) (RespostaValor, error) {
	d := NovoDecodificador(b)
	r := RespostaValor{Encontrado: d.Bool(), Item: d.Item(), Respostas: int(d.Uvarint())}
	return r, d.Err()
}

type RespostaEscrita struct {
	Versao       uint64
	Confirmacoes int
	Necessarias  int
	Replicas     int
}

func (r RespostaEscrita) Codificar() []byte {
	return NovoCodificador().U64(r.Versao).Uvarint(uint64(r.Confirmacoes)).Uvarint(uint64(r.Necessarias)).Uvarint(uint64(r.Replicas)).Bytes()
}

func DecodificarEscrita(b []byte) (RespostaEscrita, error) {
	d := NovoDecodificador(b)
	r := RespostaEscrita{Versao: d.U64(), Confirmacoes: int(d.Uvarint()), Necessarias: int(d.Uvarint()), Replicas: int(d.Uvarint())}
	return r, d.Err()
}

type PedidoScan struct {
	Prefixo        []byte
	Depois         []byte
	Limite         int
	SoChaves       bool
	IncluirLapides bool
}

func (p PedidoScan) Codificar() []byte {
	return NovoCodificador().Dados(p.Prefixo).Dados(p.Depois).Uvarint(uint64(p.Limite)).Bool(p.SoChaves).Bool(p.IncluirLapides).Bytes()
}

func DecodificarScan(b []byte) (PedidoScan, error) {
	d := NovoDecodificador(b)
	p := PedidoScan{Prefixo: d.Dados(), Depois: d.Dados(), Limite: int(d.Uvarint()), SoChaves: d.Bool(), IncluirLapides: d.Bool()}
	return p, d.Err()
}

type RespostaScan struct {
	Itens []Item
	Mais  bool
}

func (r RespostaScan) Codificar() []byte {
	c := NovoCodificador().Uvarint(uint64(len(r.Itens)))
	for _, it := range r.Itens {
		c.Item(it)
	}
	return c.Bool(r.Mais).Bytes()
}

func DecodificarRespostaScan(b []byte) (RespostaScan, error) {
	d := NovoDecodificador(b)
	n := d.Uvarint()
	if n > uint64(d.Restante()) {
		return RespostaScan{}, ErrCorpo
	}
	r := RespostaScan{Itens: make([]Item, 0, n)}
	for i := uint64(0); i < n && d.Err() == nil; i++ {
		r.Itens = append(r.Itens, d.Item())
	}
	r.Mais = d.Bool()
	return r, d.Err()
}

func CodificarErro(e *Erro) []byte {
	return NovoCodificador().Uvarint(uint64(e.Codigo)).Texto(e.Mensagem).Bytes()
}

func DecodificarErro(b []byte) *Erro {
	d := NovoDecodificador(b)
	e := &Erro{Codigo: Codigo(d.Uvarint()), Mensagem: d.Texto()}
	if d.Err() != nil {
		return &Erro{Codigo: CodigoInterno, Mensagem: "resposta de erro malformada"}
	}
	return e
}

type MembroInfo struct {
	ID            string `json:"id"`
	Endereco      string `json:"endereco"`
	Estado        string `json:"estado"`
	Geracao       uint64 `json:"geracao"`
	Desde         int64  `json:"desde"`
	UltimoContato int64  `json:"ultimo_contato"`
	Latencia      int64  `json:"latencia_us"`
	Versao        string `json:"versao"`
}

type Topologia struct {
	Cluster  string       `json:"cluster"`
	Replicas int          `json:"replicas"`
	VNodes   int          `json:"vnodes"`
	Origem   string       `json:"origem"`
	Membros  []MembroInfo `json:"membros"`
}
