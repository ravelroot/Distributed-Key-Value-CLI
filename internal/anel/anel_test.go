package anel

import (
	"fmt"
	"math"
	"testing"
)

func membros(n int) []Membro {
	var ms []Membro
	for i := 0; i < n; i++ {
		ms = append(ms, Membro{ID: fmt.Sprintf("no-%d", i+1), Endereco: fmt.Sprintf("10.0.0.%d:7400", i+1)})
	}
	return ms
}

func TestDistribuicaoEquilibrada(t *testing.T) {
	a := Novo(128)
	a.Definir(membros(5))
	cont := map[string]int{}
	const total = 50000
	for i := 0; i < total; i++ {
		cont[a.Donos([]byte(fmt.Sprintf("chave:%d", i)), 1)[0].ID]++
	}
	for id, c := range cont {
		desvio := math.Abs(float64(c)/total-0.2) / 0.2
		if desvio > 0.25 {
			t.Fatalf("%s recebeu %d chaves (desvio de %.0f%%)", id, c, desvio*100)
		}
	}
	soma := 0.0
	for _, p := range a.Participacao() {
		soma += p
	}
	if math.Abs(soma-1) > 0.0001 {
		t.Fatalf("a participação deveria somar 1, somou %f", soma)
	}
}

func TestDonosDistintos(t *testing.T) {
	a := Novo(64)
	a.Definir(membros(4))
	for i := 0; i < 1000; i++ {
		d := a.Donos([]byte(fmt.Sprintf("k%d", i)), 3)
		if len(d) != 3 || d[0].ID == d[1].ID || d[1].ID == d[2].ID || d[0].ID == d[2].ID {
			t.Fatalf("réplicas repetidas: %v", d)
		}
	}
	if d := a.Donos([]byte("x"), 10); len(d) != 4 {
		t.Fatalf("pedir mais réplicas que nós deveria limitar ao total, veio %d", len(d))
	}
}

func TestEstabilidadeAoCrescer(t *testing.T) {
	a := Novo(128)
	a.Definir(membros(4))
	b := Novo(128)
	b.Definir(membros(5))
	movidas := 0
	const total = 20000
	for i := 0; i < total; i++ {
		k := []byte(fmt.Sprintf("k%d", i))
		if a.Donos(k, 1)[0].ID != b.Donos(k, 1)[0].ID {
			movidas++
		}
	}
	frac := float64(movidas) / total
	if frac > 0.3 || frac < 0.1 {
		t.Fatalf("adicionar o quinto nó moveu %.1f%% das chaves (esperado perto de 20%%)", frac*100)
	}
}

func TestOrdemDosMembrosNaoImporta(t *testing.T) {
	a, b := Novo(32), Novo(32)
	ms := membros(3)
	a.Definir(ms)
	b.Definir([]Membro{ms[2], ms[0], ms[1]})
	for i := 0; i < 500; i++ {
		k := []byte(fmt.Sprintf("k%d", i))
		if a.Donos(k, 2)[0] != b.Donos(k, 2)[0] {
			t.Fatal("anéis com os mesmos membros devem concordar")
		}
	}
}
