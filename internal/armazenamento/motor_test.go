package armazenamento

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func abrir(t *testing.T, dir string, op Opcoes) *Motor {
	t.Helper()
	m, err := Abrir(dir, op)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func reg(k, v string, versao uint64) Registro {
	return Registro{Tipo: TipoValor, Versao: versao, Chave: []byte(k), Valor: []byte(v)}
}

func TestGravarLerEReabrir(t *testing.T) {
	dir := t.TempDir()
	m := abrir(t, dir, Opcoes{})
	for i := 0; i < 100; i++ {
		if err := m.Gravar(reg(fmt.Sprintf("k%03d", i), fmt.Sprintf("v%d", i), uint64(i+1))); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Gravar(reg("k001", "antigo", 1)); err != ErrVersaoAntiga {
		t.Fatalf("esperava ErrVersaoAntiga, veio %v", err)
	}
	m.Fechar()
	m = abrir(t, dir, Opcoes{})
	defer m.Fechar()
	r, ok, err := m.Ler([]byte("k042"))
	if err != nil || !ok || string(r.Valor) != "v42" || r.Versao != 43 {
		t.Fatalf("leitura após reabrir: %+v %v %v", r, ok, err)
	}
	if st := m.Estatisticas(); st.Chaves != 100 {
		t.Fatalf("esperava 100 chaves, veio %d", st.Chaves)
	}
}

func TestLapideETTL(t *testing.T) {
	agora := time.Now()
	m := abrir(t, t.TempDir(), Opcoes{Agora: func() time.Time { return agora }})
	defer m.Fechar()
	m.Gravar(reg("a", "1", 10))
	m.Gravar(Registro{Tipo: TipoLapide, Versao: 11, Chave: []byte("a")})
	if _, ok, _ := m.Ler([]byte("a")); ok {
		t.Fatal("a lápide deveria esconder a chave")
	}
	if r, ok, _ := m.LerBruto([]byte("a")); !ok || r.Tipo != TipoLapide {
		t.Fatal("a leitura bruta deveria devolver a lápide")
	}
	m.Gravar(Registro{Tipo: TipoValor, Versao: 12, Chave: []byte("s"), Valor: []byte("x"), Expira: agora.Add(time.Second).UnixMilli()})
	if _, ok, _ := m.Ler([]byte("s")); !ok {
		t.Fatal("a chave ainda não expirou")
	}
	agora = agora.Add(2 * time.Second)
	if _, ok, _ := m.Ler([]byte("s")); ok {
		t.Fatal("a chave deveria ter expirado")
	}
}

func TestRecuperaDeCaudaCorrompida(t *testing.T) {
	dir := t.TempDir()
	m := abrir(t, dir, Opcoes{Sincronizar: SincronizarSempre})
	m.Gravar(reg("um", "1", 1))
	m.Gravar(reg("dois", "2", 2))
	m.Fechar()
	ids, _ := (&Motor{dir: dir}).listar()
	caminho := filepath.Join(dir, nomeSegmento(ids[len(ids)-1]))
	f, _ := os.OpenFile(caminho, os.O_WRONLY|os.O_APPEND, 0o644)
	f.Write([]byte{0xde, 0xad, 0xbe, 0xef, 1, 2, 3})
	f.Close()
	m = abrir(t, dir, Opcoes{})
	defer m.Fechar()
	if st := m.Estatisticas(); st.Chaves != 2 || st.Descartados != 1 {
		t.Fatalf("recuperação: %+v", st)
	}
	if err := m.Gravar(reg("tres", "3", 3)); err != nil {
		t.Fatal(err)
	}
	if r, ok, _ := m.Ler([]byte("tres")); !ok || string(r.Valor) != "3" {
		t.Fatal("gravação depois da recuperação falhou")
	}
}

func TestCRCDetectaBitTrocado(t *testing.T) {
	r := reg("chave", "valor", 7)
	b := r.Codificar()
	b[len(b)-1] ^= 0x01
	if _, _, err := LerRegistro(bytesLeitor(b), 0); err != ErrCRC {
		t.Fatalf("esperava ErrCRC, veio %v", err)
	}
}

type bytesLeitor []byte

func (b bytesLeitor) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(b)) {
		return 0, os.ErrClosed
	}
	n := copy(p, b[off:])
	return n, nil
}

func TestCompactacao(t *testing.T) {
	agora := time.Now()
	m := abrir(t, t.TempDir(), Opcoes{SegmentoMax: 4 << 10, GracaLapide: time.Hour, Agora: func() time.Time { return agora }})
	defer m.Fechar()
	v := uint64(1) << 20
	for rodada := 0; rodada < 5; rodada++ {
		for i := 0; i < 50; i++ {
			v++
			m.Gravar(reg(fmt.Sprintf("k%02d", i), fmt.Sprintf("valor-%d-%d", rodada, i), v))
		}
	}
	velha := uint64(agora.Add(-2*time.Hour).UnixMilli()) << 16
	m.Gravar(Registro{Tipo: TipoLapide, Versao: velha + 1<<40, Chave: []byte("apagada")})
	antes := m.Estatisticas()
	res, err := m.Compactar()
	if err != nil {
		t.Fatal(err)
	}
	depois := m.Estatisticas()
	if res.BytesDepois >= res.BytesAntes || depois.Segmentos >= antes.Segmentos {
		t.Fatalf("a compactação não reduziu o disco: %+v", res)
	}
	for i := 0; i < 50; i++ {
		r, ok, err := m.Ler([]byte(fmt.Sprintf("k%02d", i)))
		if err != nil || !ok || string(r.Valor) != fmt.Sprintf("valor-4-%d", i) {
			t.Fatalf("k%02d depois da compactação: %q %v %v", i, r.Valor, ok, err)
		}
	}
}

func TestVarredura(t *testing.T) {
	m := abrir(t, t.TempDir(), Opcoes{})
	defer m.Fechar()
	for i := 0; i < 30; i++ {
		m.Gravar(reg(fmt.Sprintf("pedido:%02d", i), "x", uint64(i+1)))
		m.Gravar(reg(fmt.Sprintf("usuario:%02d", i), "y", uint64(i+100)))
	}
	regs, mais, err := m.Varrer(FiltroVarredura{Prefixo: []byte("pedido:"), Depois: []byte("pedido:09"), Limite: 5})
	if err != nil || !mais || len(regs) != 5 || string(regs[0].Chave) != "pedido:10" {
		t.Fatalf("varredura: %d %v %v", len(regs), mais, err)
	}
}
