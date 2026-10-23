package anel

import (
	"hash/fnv"
	"sort"
	"strconv"
	"sync"
)

type Membro struct {
	ID       string
	Endereco string
}

type ponto struct {
	hash uint64
	no   int
}

type Anel struct {
	mu      sync.RWMutex
	vnodes  int
	membros []Membro
	pontos  []ponto
}

func Novo(vnodes int) *Anel {
	if vnodes <= 0 {
		vnodes = 128
	}
	return &Anel{vnodes: vnodes}
}

func misturar(h uint64) uint64 {
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	return h
}

func Hash(b []byte) uint64 {
	f := fnv.New64a()
	f.Write(b)
	return misturar(f.Sum64())
}

func (a *Anel) Definir(membros []Membro) {
	ms := make([]Membro, len(membros))
	copy(ms, membros)
	sort.Slice(ms, func(i, j int) bool { return ms[i].ID < ms[j].ID })
	pontos := make([]ponto, 0, len(ms)*a.vnodes)
	for i, m := range ms {
		base := []byte(m.ID + "#")
		for v := 0; v < a.vnodes; v++ {
			pontos = append(pontos, ponto{hash: Hash(append(append([]byte{}, base...), strconv.Itoa(v)...)), no: i})
		}
	}
	sort.Slice(pontos, func(i, j int) bool {
		if pontos[i].hash == pontos[j].hash {
			return pontos[i].no < pontos[j].no
		}
		return pontos[i].hash < pontos[j].hash
	})
	a.mu.Lock()
	a.membros = ms
	a.pontos = pontos
	a.mu.Unlock()
}

func (a *Anel) Membros() []Membro {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]Membro, len(a.membros))
	copy(out, a.membros)
	return out
}

func (a *Anel) Tamanho() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.membros)
}

func (a *Anel) VNodes() int { return a.vnodes }

func (a *Anel) Donos(chave []byte, n int) []Membro {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.pontos) == 0 || n <= 0 {
		return nil
	}
	if n > len(a.membros) {
		n = len(a.membros)
	}
	h := Hash(chave)
	i := sort.Search(len(a.pontos), func(i int) bool { return a.pontos[i].hash >= h })
	vistos := make(map[int]bool, n)
	out := make([]Membro, 0, n)
	for k := 0; k < len(a.pontos) && len(out) < n; k++ {
		p := a.pontos[(i+k)%len(a.pontos)]
		if vistos[p.no] {
			continue
		}
		vistos[p.no] = true
		out = append(out, a.membros[p.no])
	}
	return out
}

func (a *Anel) Participacao() map[string]float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make(map[string]float64, len(a.membros))
	if len(a.pontos) == 0 {
		return out
	}
	const total = float64(^uint64(0))
	for i, p := range a.pontos {
		anterior := a.pontos[(i-1+len(a.pontos))%len(a.pontos)].hash
		var faixa uint64
		if i == 0 {
			faixa = p.hash + (^uint64(0) - anterior)
		} else {
			faixa = p.hash - anterior
		}
		out[a.membros[p.no].ID] += float64(faixa) / total
	}
	return out
}

type Ponto struct {
	Posicao float64 `json:"posicao"`
	No      string  `json:"no"`
}

func (a *Anel) Pontos(max int) []Ponto {
	a.mu.RLock()
	defer a.mu.RUnlock()
	passo := 1
	if max > 0 && len(a.pontos) > max {
		passo = len(a.pontos) / max
	}
	out := make([]Ponto, 0, len(a.pontos)/passo+1)
	for i := 0; i < len(a.pontos); i += passo {
		p := a.pontos[i]
		out = append(out, Ponto{Posicao: float64(p.hash) / float64(^uint64(0)), No: a.membros[p.no].ID})
	}
	return out
}
