package protocolo

import (
	"bufio"
	"bytes"
	"testing"
)

func TestQuadroIdaEVolta(t *testing.T) {
	var b bytes.Buffer
	corpo := PedidoPut{Chave: []byte("usuario:1"), Valor: []byte(`{"nome":"Ana"}`), TTL: 30000, Consistencia: ConsistenciaTodos, SeVersao: 99, SeAusente: true}.Codificar()
	if err := Escrever(&b, Quadro{Op: OpPut, ID: 7, Corpo: corpo}); err != nil {
		t.Fatal(err)
	}
	q, err := Ler(bufio.NewReader(&b))
	if err != nil || q.Op != OpPut || q.ID != 7 {
		t.Fatalf("quadro: %+v %v", q, err)
	}
	p, err := DecodificarPut(q.Corpo)
	if err != nil || string(p.Chave) != "usuario:1" || p.TTL != 30000 || p.Consistencia != ConsistenciaTodos || p.SeVersao != 99 || !p.SeAusente {
		t.Fatalf("put: %+v %v", p, err)
	}
}

func TestCorpoTruncadoNaoEntraEmPanico(t *testing.T) {
	corpo := RespostaScan{Itens: []Item{{Chave: []byte("a"), Valor: []byte("1"), Versao: 3}}, Mais: true}.Codificar()
	for i := 0; i < len(corpo); i++ {
		if _, err := DecodificarRespostaScan(corpo[:i]); err == nil {
			t.Fatalf("corpo truncado em %d bytes deveria falhar", i)
		}
	}
	r, err := DecodificarRespostaScan(corpo)
	if err != nil || len(r.Itens) != 1 || !r.Mais {
		t.Fatalf("scan: %+v %v", r, err)
	}
}

func TestRecusaQuadroGrandeEVersaoErrada(t *testing.T) {
	b := []byte{0xff, 0xff, 0xff, 0xff}
	if _, err := Ler(bufio.NewReader(bytes.NewReader(b))); err != ErrQuadroGrande {
		t.Fatalf("esperava ErrQuadroGrande, veio %v", err)
	}
	b = []byte{0, 0, 0, 7, 9, byte(OpPing), 0, 0, 0, 1, 0}
	if _, err := Ler(bufio.NewReader(bytes.NewReader(b))); err != ErrVersao {
		t.Fatalf("esperava ErrVersao, veio %v", err)
	}
}

func TestConsistencia(t *testing.T) {
	casos := map[string]int{"um": 1, "quorum": 2, "todos": 3}
	for nome, esperado := range casos {
		c, err := ParseConsistencia(nome)
		if err != nil || c.Necessarias(3) != esperado {
			t.Fatalf("%s: %d %v", nome, c.Necessarias(3), err)
		}
	}
	if _, err := ParseConsistencia("muitos"); err == nil {
		t.Fatal("consistência inválida deveria falhar")
	}
	e := CodificarErro(NovoErro(CodigoConflito, "versão %d", 3))
	if d := DecodificarErro(e); d.Codigo != CodigoConflito || d.Mensagem != "versão 3" {
		t.Fatalf("erro: %+v", d)
	}
}
