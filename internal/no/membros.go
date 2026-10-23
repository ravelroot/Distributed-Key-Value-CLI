package no

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/anel"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/protocolo"
)

const (
	EstadoVivo     = "vivo"
	EstadoSuspeito = "suspeito"
	EstadoMorto    = "morto"
	EstadoSaiu     = "saiu"
)

type membro struct {
	info protocolo.MembroInfo
}

func (n *No) topologia() protocolo.Topologia {
	n.mu.RLock()
	defer n.mu.RUnlock()
	t := protocolo.Topologia{Cluster: n.cfg.Cluster, Replicas: n.cfg.Replicas, VNodes: n.cfg.VNodes, Origem: n.cfg.ID}
	for _, m := range n.membros {
		t.Membros = append(t.Membros, m.info)
	}
	sort.Slice(t.Membros, func(i, j int) bool { return t.Membros[i].ID < t.Membros[j].ID })
	return t
}

func (n *No) Topologia() protocolo.Topologia { return n.topologia() }

func (n *No) reconstruirAnel() {
	n.mu.Lock()
	var ms []anel.Membro
	var partes []string
	for _, m := range n.membros {
		if m.info.Estado == EstadoSaiu {
			continue
		}
		ms = append(ms, anel.Membro{ID: m.info.ID, Endereco: m.info.Endereco})
		partes = append(partes, m.info.ID+"@"+m.info.Endereco)
	}
	sort.Strings(partes)
	assinatura := strings.Join(partes, ",")
	mudou := assinatura != n.assinatura
	n.assinatura = assinatura
	n.mu.Unlock()
	if !mudou {
		return
	}
	n.anel.Definir(ms)
	n.log.Info("anel atualizado", "membros", len(ms))
	if len(ms) > 1 {
		go n.redistribuir()
	}
}

func (n *No) mesclar(t protocolo.Topologia) {
	if t.Cluster != "" && t.Cluster != n.cfg.Cluster {
		return
	}
	n.mu.Lock()
	agora := time.Now().UnixMilli()
	for _, info := range t.Membros {
		if info.ID == n.cfg.ID {
			continue
		}
		atual, ok := n.membros[info.ID]
		switch {
		case !ok:
			if info.Estado == EstadoSaiu {
				continue
			}
			info.Estado = EstadoVivo
			info.UltimoContato = agora
			n.membros[info.ID] = &membro{info: info}
			n.log.Info("novo membro", "id", info.ID, "endereco", info.Endereco)
		case info.Geracao > atual.info.Geracao:
			estado := atual.info.Estado
			if info.Estado == EstadoSaiu {
				estado = EstadoSaiu
			} else if estado == EstadoSaiu || estado == EstadoMorto {
				estado = EstadoVivo
				atual.info.UltimoContato = agora
			}
			atual.info.Geracao = info.Geracao
			atual.info.Endereco = info.Endereco
			atual.info.Desde = info.Desde
			atual.info.Versao = info.Versao
			atual.info.Estado = estado
		case info.Geracao == atual.info.Geracao && info.Estado == EstadoSaiu && atual.info.Estado != EstadoSaiu:
			atual.info.Estado = EstadoSaiu
			n.log.Info("membro saiu do cluster", "id", info.ID)
		}
	}
	n.mu.Unlock()
	n.reconstruirAnel()
}

func (n *No) entrarNoCluster(ctx context.Context) {
	eu := n.topologia()
	corpo, _ := json.Marshal(eu)
	for _, s := range n.cfg.Sementes {
		s = strings.TrimSpace(s)
		if s == "" || s == n.cfg.Anunciar {
			continue
		}
		c, cancelar := context.WithTimeout(ctx, n.cfg.TempoOperacao)
		resp, err := n.pool.Chamar(c, s, protocolo.OpEntrar, corpo)
		cancelar()
		if err != nil {
			n.log.Warn("semente indisponível", "semente", s, "erro", err)
			continue
		}
		var t protocolo.Topologia
		if json.Unmarshal(resp, &t) == nil {
			n.mesclar(t)
			n.log.Info("entrou no cluster", "semente", s, "membros", len(t.Membros))
			return
		}
	}
}

func (n *No) lacoGossip() {
	defer n.laco.Done()
	t := time.NewTicker(n.cfg.IntervaloGossip)
	defer t.Stop()
	for {
		select {
		case <-n.parar:
			return
		case <-t.C:
			n.rodadaGossip()
		}
	}
}

func (n *No) rodadaGossip() {
	n.mu.RLock()
	var alvos []protocolo.MembroInfo
	for _, m := range n.membros {
		if m.info.ID != n.cfg.ID && m.info.Estado != EstadoSaiu {
			alvos = append(alvos, m.info)
		}
	}
	n.mu.RUnlock()
	eu := n.topologia()
	corpo, _ := json.Marshal(eu)
	var wg sync.WaitGroup
	for _, alvo := range alvos {
		wg.Add(1)
		go func(alvo protocolo.MembroInfo) {
			defer wg.Done()
			ctx, cancelar := context.WithTimeout(context.Background(), n.cfg.IntervaloGossip)
			defer cancelar()
			inicio := time.Now()
			resp, err := n.pool.Chamar(ctx, alvo.Endereco, protocolo.OpGossip, corpo)
			lat := time.Since(inicio)
			if err != nil {
				n.pool.Descartar(alvo.Endereco)
				n.marcarFalha(alvo.ID)
				return
			}
			var t protocolo.Topologia
			if json.Unmarshal(resp, &t) == nil {
				n.marcarVivo(alvo.ID, lat)
				n.mesclar(t)
			}
		}(alvo)
	}
	wg.Wait()
}

func (n *No) marcarVivo(id string, lat time.Duration) {
	n.mu.Lock()
	m, ok := n.membros[id]
	voltou := false
	if ok && m.info.Estado != EstadoSaiu {
		voltou = m.info.Estado == EstadoMorto || m.info.Estado == EstadoSuspeito
		m.info.Estado = EstadoVivo
		m.info.UltimoContato = time.Now().UnixMilli()
		if lat > 0 {
			m.info.Latencia = lat.Microseconds()
		}
	}
	n.mu.Unlock()
	if voltou {
		n.log.Info("membro voltou", "id", id)
		go n.entregarDicas(id)
	}
}

func (n *No) marcarFalha(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	m, ok := n.membros[id]
	if !ok || m.info.Estado == EstadoSaiu {
		return
	}
	sem := time.Since(time.UnixMilli(m.info.UltimoContato))
	anterior := m.info.Estado
	switch {
	case sem >= n.cfg.TempoMorto:
		m.info.Estado = EstadoMorto
	case sem >= n.cfg.TempoSuspeito:
		m.info.Estado = EstadoSuspeito
	}
	if anterior != m.info.Estado {
		n.log.Warn("membro mudou de estado", "id", id, "de", anterior, "para", m.info.Estado)
	}
}

func (n *No) estado(id string) string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if m, ok := n.membros[id]; ok {
		return m.info.Estado
	}
	return EstadoMorto
}

func (n *No) membrosVivos() []protocolo.MembroInfo {
	n.mu.RLock()
	defer n.mu.RUnlock()
	var out []protocolo.MembroInfo
	for _, m := range n.membros {
		if m.info.Estado == EstadoVivo || m.info.Estado == EstadoSuspeito {
			out = append(out, m.info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (n *No) Sair(ctx context.Context) {
	n.saindo.Store(true)
	n.mu.Lock()
	eu := n.membros[n.cfg.ID]
	eu.info.Estado = EstadoSaiu
	n.mu.Unlock()
	n.redistribuirTudo(ctx)
	n.rodadaGossip()
}
