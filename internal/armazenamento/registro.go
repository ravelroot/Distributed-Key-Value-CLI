package armazenamento

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

const (
	TipoValor    byte = 1
	TipoLapide   byte = 2
	tamCabecalho      = 29
	MaxChave          = 64 << 10
	MaxValor          = 32 << 20
)

var tabelaCRC = crc32.MakeTable(crc32.Castagnoli)

var (
	ErrCRC      = errors.New("registro corrompido: CRC32 não confere")
	ErrRegistro = errors.New("registro inválido")
)

type Registro struct {
	Tipo   byte
	Versao uint64
	Expira int64
	Chave  []byte
	Valor  []byte
}

func (r Registro) Tamanho() int64 {
	return int64(tamCabecalho + len(r.Chave) + len(r.Valor))
}

func (r Registro) Codificar() []byte {
	buf := make([]byte, tamCabecalho+len(r.Chave)+len(r.Valor))
	buf[4] = r.Tipo
	binary.BigEndian.PutUint64(buf[5:13], r.Versao)
	binary.BigEndian.PutUint64(buf[13:21], uint64(r.Expira))
	binary.BigEndian.PutUint32(buf[21:25], uint32(len(r.Chave)))
	binary.BigEndian.PutUint32(buf[25:29], uint32(len(r.Valor)))
	copy(buf[tamCabecalho:], r.Chave)
	copy(buf[tamCabecalho+len(r.Chave):], r.Valor)
	binary.BigEndian.PutUint32(buf[0:4], crc32.Checksum(buf[4:], tabelaCRC))
	return buf
}

func lerCabecalho(cab []byte) (tipo byte, versao uint64, expira int64, kl, vl uint32, crc uint32, err error) {
	crc = binary.BigEndian.Uint32(cab[0:4])
	tipo = cab[4]
	versao = binary.BigEndian.Uint64(cab[5:13])
	expira = int64(binary.BigEndian.Uint64(cab[13:21]))
	kl = binary.BigEndian.Uint32(cab[21:25])
	vl = binary.BigEndian.Uint32(cab[25:29])
	if (tipo != TipoValor && tipo != TipoLapide) || kl == 0 || kl > MaxChave || vl > MaxValor {
		err = ErrRegistro
	}
	return
}

func LerRegistro(r io.ReaderAt, pos int64) (Registro, int64, error) {
	cab := make([]byte, tamCabecalho)
	if _, err := r.ReadAt(cab, pos); err != nil {
		if errors.Is(err, io.EOF) {
			return Registro{}, 0, io.ErrUnexpectedEOF
		}
		return Registro{}, 0, err
	}
	tipo, versao, expira, kl, vl, crc, err := lerCabecalho(cab)
	if err != nil {
		return Registro{}, 0, err
	}
	corpo := make([]byte, int(kl)+int(vl))
	if _, err := r.ReadAt(corpo, pos+tamCabecalho); err != nil {
		if errors.Is(err, io.EOF) {
			return Registro{}, 0, io.ErrUnexpectedEOF
		}
		return Registro{}, 0, err
	}
	h := crc32.New(tabelaCRC)
	h.Write(cab[4:])
	h.Write(corpo)
	if h.Sum32() != crc {
		return Registro{}, 0, ErrCRC
	}
	reg := Registro{Tipo: tipo, Versao: versao, Expira: expira, Chave: corpo[:kl], Valor: corpo[kl:]}
	return reg, reg.Tamanho(), nil
}
