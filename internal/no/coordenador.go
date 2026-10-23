package no

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/anel"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/armazenamento"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/protocolo"
)

func (n *No) processar(ctx context.Context, q protocolo.Quadro) ([]byte, error) {
	if q.Op.Interna() && q.Op != protocolo.OpEntrar && q.Op != protocolo.OpGossip {
		n.metricas.internas.Add(1)
	}
	switch q.Op {
	case protocolo.OpPing:
		return protocolo.NovoCodificador().Texto(n.cfg.ID).Bytes(), nil
	case protocolo.OpGet:
		p, err := protocolo.DecodificarChave(q.Corpo)
		if err != nil {
			return nil, invalido(err)
		}
		r, err := n.Ler(ctx, p.Chave, p.Consistencia)
		if err != nil {
			return nil, err
		}
		return r.Codificar(), nil
	case protocolo.OpPut:
		p, err := protocolo.DecodificarPut(q.Corpo)
		if err != nil {
			return nil, invalido(err)
		}
		r, err := n.Gravar(ctx, p)
		if err != nil {
			return nil, err
		}
		return r.Codificar(), nil
	case protocolo.OpDel:
		p, err := protocolo.DecodificarChave(q.Corpo)
		if err != nil {
			return nil, invalido(err)
		}
		r, err := n.Apagar(ctx, p)
		if err != nil {
			return nil, err
		}
		return r.Codificar(), nil
	case protocolo.OpTTL:
		p, err := protocolo.DecodificarChave(q.Corpo)
		if err != nil {
			return nil, invalido(err)
		}
		r, err := n.Ler(ctx, p.Chave, p.Consistencia)
		if err != nil {
			return nil, err
		}
		r.Item.Valor = nil
		return r.Codificar(), nil
	case protocolo.OpExpirar:
		p, err := protocolo.DecodificarChave(q.Corpo)
		if err != nil {
			return nil, invalido(err)
		}
		r, err := n.Expirar(ctx, p)
		if err != nil {
			return nil, err
		}
		return r.Codificar(), nil
	case protocolo.OpScan:
		p, err := protocolo.DecodificarScan(q.Corpo)
		if err != nil {
			return nil, invalido(err)
		}
		r, err := n.Varrer(ctx, p)
		if err != nil {
			return nil, err
		}
		return r.Codificar(), nil
	case protocolo.OpMembros:
		return json.Marshal(n.topologiaCompleta())
	case protocolo.OpStats:
		return json.Marshal(n.Estatisticas())
	case protocolo.OpCompactar:
		res, err := n.motor.Compactar()
		if err != nil {
			if errors.Is(err, armazenamento.ErrCompactando) {
				return nil, protocolo.NovoErro(protocolo.CodigoConflito, "%v", err)
			}
			return nil, err
		}
		n.log.Info("compactação concluída", "antes", res.BytesAntes, "depois", res.BytesDepois, "removidos", res.Removidos, "duracao", res.Duracao)
		return json.Marshal(res)
	case protocolo.OpSair:
		go func() {
			ctx, cancelar := context.WithTimeout(context.Background(), time.Minute)
			defer cancelar()
			n.Sair(ctx)
		}()
		return nil, nil
	case protocolo.OpReplicaPut:
		it := protocolo.NovoDecodificador(q.Corpo).Item()
		return nil, n.gravarLocal(it)
	case protocolo.OpReplicaGet:
		chave := protocolo.NovoDecodificador(q.Corpo).Dados()
		reg, ok, err := n.motor.LerBruto(chave)
		if err != nil {
			return nil, err
		}
		return protocolo.RespostaValor{Encontrado: ok, Item: paraItem(reg)}.Codificar(), nil
	case protocolo.OpScanLocal:
		p, err := protocolo.DecodificarScan(q.Corpo)
		if err != nil {
			return nil, invalido(err)
		}
		r, err := n.varrerLocal(p)
		if err != nil {
			return nil, err
		}
		return r.Codificar(), nil
	case protocolo.OpGossip, protocolo.OpEntrar:
		var t protocolo.Topologia
		if err := json.Unmarshal(q.Corpo, &t); err != nil {
			return nil, invalido(err)
		}
		if t.Cluster != "" && t.Cluster != n.cfg.Cluster {
			return nil, protocolo.NovoErro(protocolo.CodigoRequisicaoInvalida, "o nó pertence ao cluster %q, não a %q", n.cfg.Cluster, t.Cluster)
		}
		if t.Origem != "" {
			n.marcarVivo(t.Origem, 0)
		}
		n.mesclar(t)
		return json.Marshal(n.topologia())
	}
	return nil, protocolo.NovoErro(protocolo.CodigoRequisicaoInvalida, "operação desconhecida: %s", q.Op)
}

func invalido(err error) error {
	return protocolo.NovoErro(protocolo.CodigoRequisicaoInvalida, "requisição inválida: %v", err)
}

func paraItem(r armazenamento.Registro) protocolo.Item {
	return protocolo.Item{Chave: r.Chave, Valor: r.Valor, Versao: r.Versao, Expira: r.Expira, Lapide: r.Tipo == armazenamento.TipoLapide}
}

func paraRegistro(it protocolo.Item) armazenamento.Registro {
	tipo := armazenamento.TipoValor
	if it.Lapide {
		tipo = armazenamento.TipoLapide
	}
	return armazenamento.Registro{Tipo: tipo, Versao: it.Versao, Expira: it.Expira, Chave: it.Chave, Valor: it.Valor}
}

func (n *No) gravarLocal(it protocolo.Item) error {
	n.relogio.Observar(it.Versao)
	err := n.motor.Gravar(paraRegistro(it))
	if errors.Is(err, armazenamento.ErrVersaoAntiga) {
		return nil
	}
	if err != nil {
		switch {
		case errors.Is(err, armazenamento.ErrChaveVazia), errors.Is(err, armazenamento.ErrChaveGrande), errors.Is(err, armazenamento.ErrValorGrande):
			return protocolo.NovoErro(protocolo.CodigoRequisicaoInvalida, "%v", err)
		}
		return protocolo.NovoErro(protocolo.CodigoInterno, "gravação local falhou em %s: %v", n.cfg.ID, err)
	}
	return nil
}

func (n *No) donos(chave []byte) []anel.Membro {
	return n.anel.Donos(chave, n.cfg.Replicas)
}

type respostaReplica struct {
	dono  anel.Membro
	item  protocolo.Item
	achou bool
	err   error
}

func (n *No) lerReplica(ctx context.Context, dono anel.Membro, chave []byte) respostaReplica {
	if dono.ID == n.cfg.ID {
		reg, ok, err := n.motor.LerBruto(chave)
		return respostaReplica{dono: dono, item: paraItem(reg), achou: ok, err: err}
	}
	if n.estado(dono.ID) == EstadoMorto {
		return respostaReplica{dono: dono, err: protocolo.NovoErro(protocolo.CodigoIndisponivel, "réplica %s fora do ar", dono.ID)}
	}
	resp, err := n.pool.Chamar(ctx, dono.Endereco, protocolo.OpReplicaGet, protocolo.NovoCodificador().Dados(chave).Bytes())
	if err != nil {
		return respostaReplica{dono: dono, err: err}
	}
	r, err := protocolo.DecodificarValor(resp)
	return respostaReplica{dono: dono, item: r.Item, achou: r.Encontrado, err: err}
}

func maisNovo(a, b protocolo.Item) bool {
	if a.Versao != b.Versao {
		return a.Versao > b.Versao
	}
	if a.Lapide != b.Lapide {
		return a.Lapide
	}
	return bytes.Compare(a.Valor, b.Valor) > 0
}

func (n *No) consistencia(c protocolo.Consistencia) protocolo.Consistencia {
	if c == protocolo.ConsistenciaPadrao {
		return protocolo.ConsistenciaQuorum
	}
	return c
}

func (n *No) Ler(ctx context.Context, chave []byte, c protocolo.Consistencia) (protocolo.RespostaValor, error) {
	if len(chave) == 0 {
		return protocolo.RespostaValor{}, protocolo.NovoErro(protocolo.CodigoRequisicaoInvalida, "informe a chave")
	}
	donos := n.donos(chave)
	if len(donos) == 0 {
		return protocolo.RespostaValor{}, protocolo.NovoErro(protocolo.CodigoIndisponivel, "o anel está vazio")
	}
	necessarias := n.consistencia(c).Necessarias(len(donos))
	ch := make(chan respostaReplica, len(donos))
	for _, d := range donos {
		go func(d anel.Membro) { ch <- n.lerReplica(ctx, d, chave) }(d)
	}
	var oks []respostaReplica
	var ultimoErr error
	recebidas := 0
	for recebidas < len(donos) && len(oks) < necessarias {
		r := <-ch
		recebidas++
		if r.err != nil {
			ultimoErr = r.err
			continue
		}
		oks = append(oks, r)
	}
	if len(oks) < necessarias {
		return protocolo.RespostaValor{}, protocolo.NovoErro(protocolo.CodigoQuorum, "leitura precisava de %d de %d réplicas e obteve %d (%v)", necessarias, len(donos), len(oks), ultimoErr)
	}
	restantes := len(donos) - recebidas
	melhor := protocolo.Item{}
	achou := false
	for _, r := range oks {
		if r.achou && (!achou || maisNovo(r.item, melhor)) {
			melhor = r.item
			achou = true
		}
	}
	go n.repararLeitura(chave, oks, ch, restantes, melhor, achou)
	agora := time.Now().UnixMilli()
	if !achou || melhor.Lapide || (melhor.Expira > 0 && agora >= melhor.Expira) {
		return protocolo.RespostaValor{Encontrado: false, Respostas: len(oks)}, protocolo.NovoErro(protocolo.CodigoNaoEncontrado, "chave não encontrada: %s", chave)
	}
	return protocolo.RespostaValor{Encontrado: true, Item: melhor, Respostas: len(oks)}, nil
}

func (n *No) repararLeitura(chave []byte, oks []respostaReplica, ch chan respostaReplica, restantes int, melhor protocolo.Item, achou bool) {
	todas := append([]respostaReplica{}, oks...)
	limite := time.After(n.cfg.TempoOperacao)
	for i := 0; i < restantes; i++ {
		select {
		case r := <-ch:
			if r.err == nil {
				todas = append(todas, r)
				if r.achou && (!achou || maisNovo(r.item, melhor)) {
					melhor = r.item
					achou = true
				}
			}
		case <-limite:
			i = restantes
		}
	}
	if !achou {
		return
	}
	for _, r := range todas {
		if r.achou && !maisNovo(melhor, r.item) {
			continue
		}
		n.metricas.reparos.Add(1)
		ctx, cancelar := context.WithTimeout(context.Background(), n.cfg.TempoOperacao)
		n.enviarReplica(ctx, r.dono, melhor)
		cancelar()
	}
}

func (n *No) enviarReplica(ctx context.Context, dono anel.Membro, it protocolo.Item) error {
	if dono.ID == n.cfg.ID {
		return n.gravarLocal(it)
	}
	_, err := n.pool.Chamar(ctx, dono.Endereco, protocolo.OpReplicaPut, protocolo.NovoCodificador().Item(it).Bytes())
	return err
}

func (n *No) replicar(ctx context.Context, chave []byte, it protocolo.Item, c protocolo.Consistencia) (protocolo.RespostaEscrita, error) {
	donos := n.donos(chave)
	if len(donos) == 0 {
		return protocolo.RespostaEscrita{}, protocolo.NovoErro(protocolo.CodigoIndisponivel, "o anel está vazio")
	}
	necessarias := n.consistencia(c).Necessarias(len(donos))
	type res struct {
		dono anel.Membro
		err  error
	}
	ch := make(chan res, len(donos))
	for _, d := range donos {
		go func(d anel.Membro) {
			if d.ID != n.cfg.ID && n.estado(d.ID) == EstadoMorto {
				ch <- res{d, protocolo.NovoErro(protocolo.CodigoIndisponivel, "réplica %s fora do ar", d.ID)}
				return
			}
			c, cancelar := context.WithTimeout(context.Background(), n.cfg.TempoOperacao)
			defer cancelar()
			ch <- res{d, n.enviarReplica(c, d, it)}
		}(d)
	}
	confirmadas := 0
	var falhas []string
	var ultimoErr error
	recebidas := 0
	esperar := func() bool {
		select {
		case r := <-ch:
			recebidas++
			if r.err != nil {
				ultimoErr = r.err
				falhas = append(falhas, r.dono.ID)
				if protocolo.CodigoDe(r.err) != protocolo.CodigoRequisicaoInvalida {
					n.dicas.adicionar(r.dono.ID, it)
				}
				return true
			}
			confirmadas++
			return true
		case <-ctx.Done():
			return false
		}
	}
	for recebidas < len(donos) && confirmadas < necessarias {
		if !esperar() {
			break
		}
	}
	if recebidas < len(donos) {
		go func(faltam int) {
			for i := 0; i < faltam; i++ {
				r := <-ch
				if r.err != nil && protocolo.CodigoDe(r.err) != protocolo.CodigoRequisicaoInvalida {
					n.dicas.adicionar(r.dono.ID, it)
				}
			}
		}(len(donos) - recebidas)
	}
	resp := protocolo.RespostaEscrita{Versao: it.Versao, Confirmacoes: confirmadas, Necessarias: necessarias, Replicas: len(donos)}
	if confirmadas < necessarias {
		if protocolo.CodigoDe(ultimoErr) == protocolo.CodigoRequisicaoInvalida {
			return resp, ultimoErr
		}
		return resp, protocolo.NovoErro(protocolo.CodigoQuorum, "escrita confirmada por %d de %d réplicas, eram necessárias %d (falharam: %v)", confirmadas, len(donos), necessarias, falhas)
	}
	return resp, nil
}

func (n *No) verificarCondicao(ctx context.Context, chave []byte, seVersao uint64, seAusente bool, c protocolo.Consistencia) (protocolo.Item, bool, error) {
	atual, err := n.Ler(ctx, chave, c)
	existe := err == nil
	if err != nil && protocolo.CodigoDe(err) != protocolo.CodigoNaoEncontrado {
		return protocolo.Item{}, false, err
	}
	if seAusente && existe {
		return atual.Item, existe, protocolo.NovoErro(protocolo.CodigoConflito, "a chave %s já existe (versão %d)", chave, atual.Item.Versao)
	}
	if seVersao != 0 && (!existe || atual.Item.Versao != seVersao) {
		v := uint64(0)
		if existe {
			v = atual.Item.Versao
		}
		return atual.Item, existe, protocolo.NovoErro(protocolo.CodigoConflito, "versão esperada %d, versão atual %d", seVersao, v)
	}
	return atual.Item, existe, nil
}

func (n *No) Gravar(ctx context.Context, p protocolo.PedidoPut) (protocolo.RespostaEscrita, error) {
	if len(p.Chave) == 0 {
		return protocolo.RespostaEscrita{}, protocolo.NovoErro(protocolo.CodigoRequisicaoInvalida, "informe a chave")
	}
	if len(p.Chave) > armazenamento.MaxChave || len(p.Valor) > armazenamento.MaxValor {
		return protocolo.RespostaEscrita{}, protocolo.NovoErro(protocolo.CodigoRequisicaoInvalida, "chave ou valor acima do limite (chave %d bytes, valor %d bytes)", armazenamento.MaxChave, armazenamento.MaxValor)
	}
	if p.TTL < 0 {
		return protocolo.RespostaEscrita{}, protocolo.NovoErro(protocolo.CodigoRequisicaoInvalida, "o TTL não pode ser negativo")
	}
	if p.SeVersao != 0 || p.SeAusente {
		if _, _, err := n.verificarCondicao(ctx, p.Chave, p.SeVersao, p.SeAusente, protocolo.ConsistenciaQuorum); err != nil {
			return protocolo.RespostaEscrita{}, err
		}
	}
	it := protocolo.Item{Chave: p.Chave, Valor: p.Valor, Versao: n.relogio.Proxima()}
	if p.TTL > 0 {
		it.Expira = time.Now().UnixMilli() + p.TTL
	}
	return n.replicar(ctx, p.Chave, it, p.Consistencia)
}

func (n *No) Apagar(ctx context.Context, p protocolo.PedidoChave) (protocolo.RespostaEscrita, error) {
	if len(p.Chave) == 0 {
		return protocolo.RespostaEscrita{}, protocolo.NovoErro(protocolo.CodigoRequisicaoInvalida, "informe a chave")
	}
	if p.SeVersao != 0 {
		if _, _, err := n.verificarCondicao(ctx, p.Chave, p.SeVersao, false, protocolo.ConsistenciaQuorum); err != nil {
			return protocolo.RespostaEscrita{}, err
		}
	}
	it := protocolo.Item{Chave: p.Chave, Versao: n.relogio.Proxima(), Lapide: true}
	return n.replicar(ctx, p.Chave, it, p.Consistencia)
}

func (n *No) Expirar(ctx context.Context, p protocolo.PedidoChave) (protocolo.RespostaEscrita, error) {
	atual, _, err := n.verificarCondicao(ctx, p.Chave, 0, false, protocolo.ConsistenciaQuorum)
	if err != nil {
		return protocolo.RespostaEscrita{}, err
	}
	if atual.Versao == 0 || atual.Lapide {
		return protocolo.RespostaEscrita{}, protocolo.NovoErro(protocolo.CodigoNaoEncontrado, "chave não encontrada: %s", p.Chave)
	}
	it := protocolo.Item{Chave: p.Chave, Valor: atual.Valor, Versao: n.relogio.Proxima()}
	if p.TTL > 0 {
		it.Expira = time.Now().UnixMilli() + p.TTL
	}
	return n.replicar(ctx, p.Chave, it, p.Consistencia)
}

func (n *No) possui(chave []byte) bool {
	for _, d := range n.donos(chave) {
		if d.ID == n.cfg.ID {
			return true
		}
	}
	return false
}

func (n *No) varrerLocal(p protocolo.PedidoScan) (protocolo.RespostaScan, error) {
	limite := p.Limite
	if limite <= 0 || limite > 10000 {
		limite = 10000
	}
	regs, _, err := n.motor.Varrer(armazenamento.FiltroVarredura{Prefixo: p.Prefixo, Depois: p.Depois, IncluirLapides: true, SoChaves: p.SoChaves})
	if err != nil {
		return protocolo.RespostaScan{}, err
	}
	out := protocolo.RespostaScan{}
	for _, r := range regs {
		if !n.possui(r.Chave) {
			continue
		}
		if len(out.Itens) == limite {
			out.Mais = true
			break
		}
		out.Itens = append(out.Itens, paraItem(r))
	}
	return out, nil
}

type rodadaScan struct {
	itens  map[string]protocolo.Item
	limite string
	mais   bool
	falhas []string
	vivos  int
}

func (n *No) rodadaVarredura(ctx context.Context, p protocolo.PedidoScan) rodadaScan {
	vivos := n.membrosVivos()
	type res struct {
		r   protocolo.RespostaScan
		err error
		id  string
	}
	ch := make(chan res, len(vivos))
	for _, m := range vivos {
		go func(m protocolo.MembroInfo) {
			if m.ID == n.cfg.ID {
				r, err := n.varrerLocal(p)
				ch <- res{r, err, m.ID}
				return
			}
			resp, err := n.pool.Chamar(ctx, m.Endereco, protocolo.OpScanLocal, p.Codificar())
			if err != nil {
				ch <- res{err: err, id: m.ID}
				return
			}
			r, err := protocolo.DecodificarRespostaScan(resp)
			ch <- res{r, err, m.ID}
		}(m)
	}
	rod := rodadaScan{itens: make(map[string]protocolo.Item), vivos: len(vivos)}
	for range vivos {
		r := <-ch
		if r.err != nil {
			rod.falhas = append(rod.falhas, r.id)
			continue
		}
		if r.r.Mais && len(r.r.Itens) > 0 {
			ult := string(r.r.Itens[len(r.r.Itens)-1].Chave)
			if !rod.mais || ult < rod.limite {
				rod.limite = ult
			}
			rod.mais = true
		}
		for _, it := range r.r.Itens {
			k := string(it.Chave)
			if atual, ok := rod.itens[k]; !ok || maisNovo(it, atual) {
				rod.itens[k] = it
			}
		}
	}
	return rod
}

func (n *No) Varrer(ctx context.Context, p protocolo.PedidoScan) (protocolo.RespostaScan, error) {
	if p.Limite <= 0 {
		p.Limite = 100
	}
	if p.Limite > 10000 {
		p.Limite = 10000
	}
	local := p
	local.IncluirLapides = true
	if local.Limite < 64 {
		local.Limite = 64
	}
	out := protocolo.RespostaScan{}
	agora := time.Now().UnixMilli()
	for rodada := 0; rodada < 64; rodada++ {
		rod := n.rodadaVarredura(ctx, local)
		if len(rod.falhas) > 0 && len(rod.falhas) == rod.vivos {
			return protocolo.RespostaScan{}, protocolo.NovoErro(protocolo.CodigoQuorum, "nenhum nó respondeu à varredura (%v)", rod.falhas)
		}
		chaves := make([]string, 0, len(rod.itens))
		for k := range rod.itens {
			if rod.mais && k > rod.limite {
				continue
			}
			chaves = append(chaves, k)
		}
		sort.Strings(chaves)
		for _, k := range chaves {
			it := rod.itens[k]
			if !p.IncluirLapides && (it.Lapide || (it.Expira > 0 && agora >= it.Expira)) {
				continue
			}
			if len(out.Itens) == p.Limite {
				out.Mais = true
				return out, nil
			}
			if p.SoChaves {
				it.Valor = nil
			}
			out.Itens = append(out.Itens, it)
		}
		if !rod.mais || ctx.Err() != nil {
			return out, nil
		}
		local.Depois = []byte(rod.limite)
		if local.Limite < 4096 {
			local.Limite *= 2
		}
	}
	out.Mais = true
	return out, nil
}

func (n *No) redistribuir() {
	ctx, cancelar := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelar()
	n.redistribuirTudo(ctx)
}

func (n *No) redistribuirTudo(ctx context.Context) {
	n.redist.Lock()
	defer n.redist.Unlock()
	if n.parado.Load() {
		return
	}
	if n.saindo.Load() {
		n.reconstruirAnelSemMim()
	}
	enviados := 0
	n.motor.Percorrer(func(r armazenamento.Registro) bool {
		if ctx.Err() != nil || n.parado.Load() {
			return false
		}
		it := paraItem(r)
		for _, d := range n.donos(r.Chave) {
			if d.ID == n.cfg.ID {
				continue
			}
			if err := n.enviarReplica(ctx, d, it); err != nil {
				n.dicas.adicionar(d.ID, it)
			}
			enviados++
		}
		return true
	})
	if enviados > 0 {
		n.metricas.transferidas.Add(uint64(enviados))
		n.log.Info("redistribuição concluída", "registros_enviados", enviados)
	}
}

func (n *No) reconstruirAnelSemMim() {
	n.mu.RLock()
	var ms []anel.Membro
	for _, m := range n.membros {
		if m.info.Estado != EstadoSaiu && m.info.ID != n.cfg.ID {
			ms = append(ms, anel.Membro{ID: m.info.ID, Endereco: m.info.Endereco})
		}
	}
	n.mu.RUnlock()
	n.anel.Definir(ms)
}

func (n *No) Donos(chave []byte) []anel.Membro { return n.donos(chave) }
