package no

import (
	"context"
	"sync"
	"time"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/anel"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/protocolo"
)

type dicas struct {
	mu          sync.Mutex
	max         int
	total       int
	por         map[string]map[string]protocolo.Item
	descartadas uint64
	entregues   uint64
}

func novasDicas(max int) *dicas {
	return &dicas{max: max, por: make(map[string]map[string]protocolo.Item)}
}

func (d *dicas) adicionar(destino string, it protocolo.Item) {
	d.mu.Lock()
	defer d.mu.Unlock()
	fila, ok := d.por[destino]
	if !ok {
		fila = make(map[string]protocolo.Item)
		d.por[destino] = fila
	}
	k := string(it.Chave)
	if atual, ok := fila[k]; ok {
		if maisNovo(it, atual) {
			fila[k] = it
		}
		return
	}
	if d.total >= d.max {
		d.descartadas++
		return
	}
	fila[k] = it
	d.total++
}

func (d *dicas) retirar(destino string) []protocolo.Item {
	d.mu.Lock()
	defer d.mu.Unlock()
	fila := d.por[destino]
	if len(fila) == 0 {
		return nil
	}
	out := make([]protocolo.Item, 0, len(fila))
	for _, it := range fila {
		out = append(out, it)
	}
	d.total -= len(fila)
	delete(d.por, destino)
	return out
}

func (d *dicas) pendentes() map[string]int {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]int, len(d.por))
	for k, v := range d.por {
		out[k] = len(v)
	}
	return out
}

func (d *dicas) contagem() (int, uint64, uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.total, d.entregues, d.descartadas
}

func (n *No) lacoDicas() {
	defer n.laco.Done()
	t := time.NewTicker(2 * n.cfg.IntervaloGossip)
	defer t.Stop()
	for {
		select {
		case <-n.parar:
			return
		case <-t.C:
			for destino := range n.dicas.pendentes() {
				if n.estado(destino) == EstadoVivo {
					n.entregarDicas(destino)
				}
			}
		}
	}
}

func (n *No) entregarDicas(destino string) {
	itens := n.dicas.retirar(destino)
	if len(itens) == 0 {
		return
	}
	n.mu.RLock()
	m, ok := n.membros[destino]
	var alvo anel.Membro
	if ok {
		alvo = anel.Membro{ID: m.info.ID, Endereco: m.info.Endereco}
	}
	n.mu.RUnlock()
	if !ok {
		return
	}
	entregues := 0
	for i, it := range itens {
		ctx, cancelar := context.WithTimeout(context.Background(), n.cfg.TempoOperacao)
		err := n.enviarReplica(ctx, alvo, it)
		cancelar()
		if err != nil {
			for _, resto := range itens[i:] {
				n.dicas.adicionar(destino, resto)
			}
			break
		}
		entregues++
	}
	n.dicas.mu.Lock()
	n.dicas.entregues += uint64(entregues)
	n.dicas.mu.Unlock()
	if entregues > 0 {
		n.log.Info("dicas entregues", "destino", destino, "quantidade", entregues)
	}
}
