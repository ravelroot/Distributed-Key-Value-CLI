package comandos

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/cliente"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/saida"
)

// Tentativas de uma operação de ler-e-gravar antes de desistir por conflito.
const tentativasCAS = 25

func init() {
	flagTTLCriar := Flag{Nome: "ttl", Curta: "t", Ajuda: "TTL aplicado quando a chave é criada", Tipo: FlagDuracao}
	registrar(&Comando{
		Nome: "incr", Apelidos: []string{"incrementar"}, Uso: "<chave> [delta]", Grupo: "Dados", Min: 1, Max: 2,
		Resumo:    "Soma ao contador guardado na chave (padrão 1)",
		Descricao: "Lê o valor, soma o delta e grava com escrita condicional pela versão lida. Se outra escrita chegar antes, tenta de novo. Uma chave ausente começa em 0. O valor precisa ser um número inteiro em texto.",
		Exemplos:  []string{"leme incr visitas:home", "leme incr estoque:7 -5", "leme incr limite:ip:10.0.0.4 --ttl 1m"},
		Flags:     []Flag{flagConsistencia, flagTTLCriar},
		Exec:      func(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) { return execIncr(ctx, s, a, 1) },
	})
	registrar(&Comando{
		Nome: "decr", Apelidos: []string{"decrementar"}, Uso: "<chave> [delta]", Grupo: "Dados", Min: 1, Max: 2,
		Resumo:   "Subtrai do contador guardado na chave (padrão 1)",
		Exemplos: []string{"leme decr estoque:7", "leme decr creditos:42 10"},
		Flags:    []Flag{flagConsistencia, flagTTLCriar},
		Exec:     func(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) { return execIncr(ctx, s, a, -1) },
	})
	registrar(&Comando{
		Nome: "copy", Apelidos: []string{"copiar", "cp"}, Uso: "<origem> <destino>", Grupo: "Dados", Min: 2, Max: 2,
		Resumo:    "Copia o valor e o TTL de uma chave para outra",
		Descricao: "Sem --substituir, a cópia só acontece se o destino não existir (código de saída 6 se existir).",
		Exemplos:  []string{"leme copy config:tema config:tema:backup", "leme copy usuario:42 usuario:43 --substituir"},
		Flags:     []Flag{flagConsistencia, {Nome: "substituir", Curta: "s", Ajuda: "sobrescreve o destino se existir", Tipo: FlagBooleano}},
		Exec: func(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
			return execCopiar(ctx, s, a, false)
		},
	})
	registrar(&Comando{
		Nome: "rename", Apelidos: []string{"renomear", "mv"}, Uso: "<origem> <destino>", Grupo: "Dados", Min: 2, Max: 2,
		Resumo:    "Move uma chave para outro nome, com valor e TTL",
		Descricao: "Copia para o destino e apaga a origem condicionada à versão lida, então uma escrita que chegue no meio não se perde.",
		Exemplos:  []string{"leme rename sessao:tmp sessao:9f3", "leme rename pedido:rascunho pedido:0042 --substituir"},
		Flags:     []Flag{flagConsistencia, {Nome: "substituir", Curta: "s", Ajuda: "sobrescreve o destino se existir", Tipo: FlagBooleano}},
		Exec: func(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
			return execCopiar(ctx, s, a, true)
		},
	})
	registrar(&Comando{
		Nome: "mget", Apelidos: []string{"ler-varios"}, Uso: "<chave>...", Grupo: "Dados", Min: 1, Max: -1,
		Resumo:   "Lê várias chaves em paralelo",
		Exemplos: []string{"leme mget usuario:1 usuario:2 usuario:3", "leme keys config: -o texto | xargs leme mget -o json"},
		Flags:    []Flag{flagConsistencia},
		Exec:     execMget,
	})
	registrar(&Comando{
		Nome: "mset", Apelidos: []string{"gravar-varios"}, Uso: "<chave=valor>...", Grupo: "Dados", Min: 1, Max: -1,
		Resumo:   "Grava várias chaves em paralelo",
		Exemplos: []string{"leme mset config:tema=escuro config:idioma=pt-BR", "leme mset sessao:a=1 sessao:b=2 --ttl 10m"},
		Flags:    []Flag{flagConsistencia, {Nome: "ttl", Curta: "t", Ajuda: "expira depois da duração", Tipo: FlagDuracao}},
		Exec:     execMset,
	})
}

func execIncr(ctx context.Context, s *Sessao, a Args, sinal int64) (*saida.Resultado, error) {
	c, err := a.consistencia(s)
	if err != nil {
		return nil, err
	}
	delta := int64(1)
	if len(a.Pos) > 1 {
		delta, err = strconv.ParseInt(a.Pos[1], 10, 64)
		if err != nil {
			return nil, Uso("delta inválido %q: use um número inteiro", a.Pos[1])
		}
	}
	delta *= sinal
	ttl, err := a.Duracao("ttl")
	if err != nil {
		return nil, err
	}
	chave := []byte(a.Pos[0])
	for tentativa := 1; tentativa <= tentativasCAS; tentativa++ {
		v, err := s.Cliente.Get(ctx, chave, c)
		ausente := CodigoSaida(err) == SaidaNaoEncontrado
		if err != nil && !ausente {
			return nil, err
		}
		atual := int64(0)
		op := cliente.OpcoesPut{Consistencia: c}
		if ausente {
			op.SeAusente = true
			op.TTL = ttl
		} else {
			atual, err = strconv.ParseInt(strings.TrimSpace(string(v.Valor)), 10, 64)
			if err != nil {
				return nil, &ErroSaida{Codigo: SaidaUso, Msg: fmt.Sprintf("o valor de %s não é um número inteiro", a.Pos[0])}
			}
			op.SeVersao = v.Versao
			op.TTL = v.TTL()
		}
		novo := atual + delta
		e, err := s.Cliente.Put(ctx, chave, []byte(strconv.FormatInt(novo, 10)), op)
		if CodigoSaida(err) == SaidaConflito {
			select {
			case <-time.After(time.Duration(tentativa) * 2 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		texto := strconv.FormatInt(novo, 10)
		return &saida.Resultado{Bruto: []byte(texto), Dados: map[string]any{"chave": a.Pos[0], "valor": novo, "anterior": atual, "versao": strconv.FormatUint(e.Versao, 10), "tentativas": tentativa}}, nil
	}
	return nil, &ErroSaida{Codigo: SaidaConflito, Msg: fmt.Sprintf("%s mudou %d vezes seguidas; tente de novo", a.Pos[0], tentativasCAS)}
}

func execCopiar(ctx context.Context, s *Sessao, a Args, mover bool) (*saida.Resultado, error) {
	c, err := a.consistencia(s)
	if err != nil {
		return nil, err
	}
	origem, destino := a.Pos[0], a.Pos[1]
	if origem == destino {
		return nil, Uso("origem e destino são a mesma chave")
	}
	v, err := s.Cliente.Get(ctx, []byte(origem), c)
	if err != nil {
		return nil, err
	}
	if v.Expira > 0 && v.TTL() == 0 {
		return nil, &ErroSaida{Codigo: SaidaNaoEncontrado, Msg: origem + " acabou de expirar"}
	}
	op := cliente.OpcoesPut{Consistencia: c, TTL: v.TTL(), SeAusente: !a.Bool("substituir")}
	e, err := s.Cliente.Put(ctx, []byte(destino), v.Valor, op)
	if err != nil {
		if CodigoSaida(err) == SaidaConflito && op.SeAusente {
			return nil, &ErroSaida{Codigo: SaidaConflito, Msg: destino + " já existe; use --substituir para sobrescrever"}
		}
		return nil, err
	}
	verbo := "copiada"
	if mover {
		if _, err := s.Cliente.Del(ctx, []byte(origem), c, v.Versao); err != nil {
			if CodigoSaida(err) == SaidaConflito {
				return nil, &ErroSaida{Codigo: SaidaConflito, Msg: origem + " mudou durante a operação; o destino foi gravado e a origem foi mantida"}
			}
			return nil, err
		}
		verbo = "movida"
	}
	return &saida.Resultado{
		Mensagem: fmt.Sprintf("%s %s %s para %s", s.Estilo.Verde("✓"), origem, verbo, destino),
		Dados:    map[string]any{"origem": origem, "destino": destino, "versao": strconv.FormatUint(e.Versao, 10), "movida": mover, "ttl_ms": v.TTL().Milliseconds()},
	}, nil
}

func paralelo(n int, f func(i int)) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer func() { <-sem; wg.Done() }()
			f(i)
		}(i)
	}
	wg.Wait()
}

func execMget(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	c, err := a.consistencia(s)
	if err != nil {
		return nil, err
	}
	vals := make([]cliente.Valor, len(a.Pos))
	errs := make([]error, len(a.Pos))
	paralelo(len(a.Pos), func(i int) { vals[i], errs[i] = s.Cliente.Get(ctx, []byte(a.Pos[i]), c) })
	g := &saida.Grade{Colunas: []string{"CHAVE", "VALOR", "VERSÃO", "TTL"}, Direita: []int{2}}
	var dados []map[string]any
	var bruto []byte
	achadas := 0
	for i, k := range a.Pos {
		d := map[string]any{"chave": k}
		switch {
		case errs[i] == nil:
			achadas++
			g.Linhas = append(g.Linhas, []string{k, saida.Resumir(vals[i].Valor, 60), strconv.FormatUint(vals[i].Versao, 10), textoTTL(vals[i].Expira)})
			d["valor"] = saida.Exibivel(vals[i].Valor)
			d["versao"] = strconv.FormatUint(vals[i].Versao, 10)
			bruto = append(bruto, vals[i].Valor...)
		case CodigoSaida(errs[i]) == SaidaNaoEncontrado:
			g.Linhas = append(g.Linhas, []string{k, s.Estilo.Fraco("(ausente)"), "", ""})
			d["ausente"] = true
		default:
			g.Linhas = append(g.Linhas, []string{k, s.Estilo.Vermelho(errs[i].Error()), "", ""})
			d["erro"] = errs[i].Error()
		}
		bruto = append(bruto, '\n')
		dados = append(dados, d)
	}
	r := &saida.Resultado{Grade: g, Bruto: bruto, Dados: dados, Rodape: fmt.Sprintf("%d de %d chaves encontradas", achadas, len(a.Pos))}
	if achadas == 0 {
		for _, e := range errs {
			if CodigoSaida(e) != SaidaNaoEncontrado {
				return r, e
			}
		}
		return r, &ErroSaida{Codigo: SaidaNaoEncontrado, Msg: "nenhuma chave encontrada"}
	}
	return r, nil
}

func execMset(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	c, err := a.consistencia(s)
	if err != nil {
		return nil, err
	}
	ttl, err := a.Duracao("ttl")
	if err != nil {
		return nil, err
	}
	type par struct{ k, v string }
	pares := make([]par, len(a.Pos))
	for i, p := range a.Pos {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, Uso("%q não está no formato chave=valor", p)
		}
		pares[i] = par{k, v}
	}
	errs := make([]error, len(pares))
	paralelo(len(pares), func(i int) {
		_, errs[i] = s.Cliente.Put(ctx, []byte(pares[i].k), []byte(pares[i].v), cliente.OpcoesPut{Consistencia: c, TTL: ttl})
	})
	g := &saida.Grade{Colunas: []string{"", "CHAVE", "RESULTADO"}}
	ok := 0
	var primeiro error
	for i, p := range pares {
		if errs[i] == nil {
			ok++
			g.Linhas = append(g.Linhas, []string{s.Estilo.Ponto("ok"), p.k, "gravada"})
		} else {
			if primeiro == nil {
				primeiro = errs[i]
			}
			g.Linhas = append(g.Linhas, []string{s.Estilo.Ponto("erro"), p.k, errs[i].Error()})
		}
	}
	r := &saida.Resultado{Grade: g, Dados: map[string]any{"gravadas": ok, "falhas": len(pares) - ok}, Rodape: fmt.Sprintf("%d de %d chaves gravadas", ok, len(pares))}
	return r, primeiro
}
