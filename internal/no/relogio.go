package no

import (
	"sync"
	"time"
)

type Relogio struct {
	mu     sync.Mutex
	ultimo uint64
	agora  func() time.Time
}

func NovoRelogio(agora func() time.Time) *Relogio {
	if agora == nil {
		agora = time.Now
	}
	return &Relogio{agora: agora}
}

func (r *Relogio) Proxima() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	fisico := uint64(r.agora().UnixMilli()) << 16
	if fisico > r.ultimo {
		r.ultimo = fisico
	} else {
		r.ultimo++
	}
	return r.ultimo
}

func (r *Relogio) Observar(v uint64) {
	r.mu.Lock()
	if v > r.ultimo {
		r.ultimo = v
	}
	r.mu.Unlock()
}
