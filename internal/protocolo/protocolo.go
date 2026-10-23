package protocolo

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

const (
	Versao        byte   = 1
	TamanhoMaximo uint32 = 64 << 20
	cabecalho            = 7
)

type Op byte

const (
	OpPing Op = iota + 1
	OpAuth
	OpGet
	OpPut
	OpDel
	OpScan
	OpMembros
	OpStats
	OpCompactar
	OpSair
	OpTTL
	OpExpirar
	OpReplicaPut
	OpReplicaGet
	OpScanLocal
	OpGossip
	OpEntrar
	OpResposta Op = 0x7E
	OpErro     Op = 0x7F
)

var nomesOps = map[Op]string{
	OpPing: "PING", OpAuth: "AUTH", OpGet: "GET", OpPut: "PUT", OpDel: "DEL", OpScan: "SCAN",
	OpMembros: "MEMBROS", OpStats: "STATS", OpCompactar: "COMPACTAR", OpSair: "SAIR",
	OpTTL: "TTL", OpExpirar: "EXPIRAR", OpReplicaPut: "REPLICA_PUT", OpReplicaGet: "REPLICA_GET",
	OpScanLocal: "SCAN_LOCAL", OpGossip: "GOSSIP", OpEntrar: "ENTRAR", OpResposta: "RESPOSTA", OpErro: "ERRO",
}

func (o Op) String() string {
	if n, ok := nomesOps[o]; ok {
		return n
	}
	return fmt.Sprintf("OP(%d)", byte(o))
}

func (o Op) Interna() bool {
	return o >= OpReplicaPut && o <= OpEntrar
}

type Codigo uint16

const (
	CodigoOK Codigo = iota
	CodigoNaoEncontrado
	CodigoRequisicaoInvalida
	CodigoNaoAutorizado
	CodigoQuorum
	CodigoConflito
	CodigoIndisponivel
	CodigoInterno
	CodigoTempoEsgotado
)

var nomesCodigos = map[Codigo]string{
	CodigoOK: "ok", CodigoNaoEncontrado: "nao_encontrado", CodigoRequisicaoInvalida: "requisicao_invalida",
	CodigoNaoAutorizado: "nao_autorizado", CodigoQuorum: "quorum_nao_atingido", CodigoConflito: "conflito_de_versao",
	CodigoIndisponivel: "indisponivel", CodigoInterno: "erro_interno", CodigoTempoEsgotado: "tempo_esgotado",
}

func (c Codigo) String() string {
	if n, ok := nomesCodigos[c]; ok {
		return n
	}
	return fmt.Sprintf("codigo_%d", uint16(c))
}

type Erro struct {
	Codigo   Codigo
	Mensagem string
}

func (e *Erro) Error() string {
	if e.Mensagem == "" {
		return e.Codigo.String()
	}
	return e.Mensagem
}

func NovoErro(c Codigo, formato string, args ...any) *Erro {
	return &Erro{Codigo: c, Mensagem: fmt.Sprintf(formato, args...)}
}

func CodigoDe(err error) Codigo {
	var e *Erro
	if errors.As(err, &e) {
		return e.Codigo
	}
	return CodigoInterno
}

type Quadro struct {
	Op    Op
	ID    uint32
	Corpo []byte
}

var ErrQuadroGrande = errors.New("quadro maior que o limite do protocolo")
var ErrVersao = errors.New("versão do protocolo incompatível")

func Escrever(w io.Writer, q Quadro) error {
	tam := uint32(cabecalho + len(q.Corpo))
	if tam > TamanhoMaximo {
		return ErrQuadroGrande
	}
	buf := make([]byte, 4+cabecalho, 4+cabecalho+len(q.Corpo))
	binary.BigEndian.PutUint32(buf[0:4], tam)
	buf[4] = Versao
	buf[5] = byte(q.Op)
	binary.BigEndian.PutUint32(buf[6:10], q.ID)
	buf[10] = 0
	buf = append(buf, q.Corpo...)
	_, err := w.Write(buf)
	return err
}

func Ler(r *bufio.Reader) (Quadro, error) {
	var tb [4]byte
	if _, err := io.ReadFull(r, tb[:]); err != nil {
		return Quadro{}, err
	}
	tam := binary.BigEndian.Uint32(tb[:])
	if tam > TamanhoMaximo {
		return Quadro{}, ErrQuadroGrande
	}
	if tam < cabecalho {
		return Quadro{}, io.ErrUnexpectedEOF
	}
	dados := make([]byte, tam)
	if _, err := io.ReadFull(r, dados); err != nil {
		return Quadro{}, err
	}
	if dados[0] != Versao {
		return Quadro{}, ErrVersao
	}
	return Quadro{Op: Op(dados[1]), ID: binary.BigEndian.Uint32(dados[2:6]), Corpo: dados[cabecalho:]}, nil
}

type Codificador struct {
	b []byte
}

func NovoCodificador() *Codificador { return &Codificador{b: make([]byte, 0, 64)} }

func (c *Codificador) Bytes() []byte { return c.b }

func (c *Codificador) Uvarint(v uint64) *Codificador {
	c.b = binary.AppendUvarint(c.b, v)
	return c
}

func (c *Codificador) Varint(v int64) *Codificador {
	c.b = binary.AppendVarint(c.b, v)
	return c
}

func (c *Codificador) U64(v uint64) *Codificador {
	c.b = binary.BigEndian.AppendUint64(c.b, v)
	return c
}

func (c *Codificador) F64(v float64) *Codificador {
	return c.U64(math.Float64bits(v))
}

func (c *Codificador) Bool(v bool) *Codificador {
	if v {
		c.b = append(c.b, 1)
	} else {
		c.b = append(c.b, 0)
	}
	return c
}

func (c *Codificador) Byte(v byte) *Codificador {
	c.b = append(c.b, v)
	return c
}

func (c *Codificador) Dados(v []byte) *Codificador {
	c.Uvarint(uint64(len(v)))
	c.b = append(c.b, v...)
	return c
}

func (c *Codificador) Texto(v string) *Codificador {
	c.Uvarint(uint64(len(v)))
	c.b = append(c.b, v...)
	return c
}

type Decodificador struct {
	b   []byte
	err error
}

var ErrCorpo = errors.New("corpo da mensagem malformado")

func NovoDecodificador(b []byte) *Decodificador { return &Decodificador{b: b} }

func (d *Decodificador) Err() error { return d.err }

func (d *Decodificador) Restante() int { return len(d.b) }

func (d *Decodificador) falhar() {
	if d.err == nil {
		d.err = ErrCorpo
	}
	d.b = nil
}

func (d *Decodificador) Uvarint() uint64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.b)
	if n <= 0 {
		d.falhar()
		return 0
	}
	d.b = d.b[n:]
	return v
}

func (d *Decodificador) Varint() int64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Varint(d.b)
	if n <= 0 {
		d.falhar()
		return 0
	}
	d.b = d.b[n:]
	return v
}

func (d *Decodificador) U64() uint64 {
	if d.err != nil || len(d.b) < 8 {
		d.falhar()
		return 0
	}
	v := binary.BigEndian.Uint64(d.b)
	d.b = d.b[8:]
	return v
}

func (d *Decodificador) F64() float64 { return math.Float64frombits(d.U64()) }

func (d *Decodificador) Byte() byte {
	if d.err != nil || len(d.b) < 1 {
		d.falhar()
		return 0
	}
	v := d.b[0]
	d.b = d.b[1:]
	return v
}

func (d *Decodificador) Bool() bool { return d.Byte() == 1 }

func (d *Decodificador) Dados() []byte {
	n := d.Uvarint()
	if d.err != nil {
		return nil
	}
	if uint64(len(d.b)) < n {
		d.falhar()
		return nil
	}
	v := make([]byte, n)
	copy(v, d.b[:n])
	d.b = d.b[n:]
	return v
}

func (d *Decodificador) Texto() string { return string(d.Dados()) }
