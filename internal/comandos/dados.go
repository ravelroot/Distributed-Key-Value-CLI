package comandos

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/cliente"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/protocolo"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/saida"
)

func init() {
	registrar(&Comando{
		Nome: "get", Apelidos: []string{"ler", "obter"}, Uso: "<chave>", Grupo: "Dados", Min: 1, Max: 1,
		Resumo:    "Lê o valor de uma chave",
		Descricao: "Consulta as réplicas da chave e devolve a versão mais recente. Com --detalhes mostra versão, TTL, réplicas que responderam e o nó coordenador.",
		Exemplos:  []string{"leme get usuario:42", "leme get usuario:42 --detalhes -c todos", "leme get config:tema -o json"},
		Flags:     []Flag{flagConsistencia, {Nome: "detalhes", Curta: "d", Ajuda: "mostra versão, TTL e réplicas", Tipo: FlagBooleano}},
		Exec:      execGet,
	})
	registrar(&Comando{
		Nome: "put", Apelidos: []string{"set", "gravar"}, Uso: "<chave> <valor | ->", Grupo: "Dados", Min: 1, Max: 2,
		Resumo:    "Grava um valor (use - para ler da entrada padrão)",
		Descricao: "A escrita recebe uma versão nova do relógio híbrido do coordenador e é confirmada quando a consistência pedida é atingida. --se-versao e --se-ausente fazem escritas condicionais.",
		Exemplos:  []string{"leme put usuario:42 '{\"nome\":\"Ana\"}'", "leme put sessao:9f3 ativo --ttl 30m", "cat foto.png | leme put arquivo:foto -", "leme put estoque:7 9 --se-versao 1820473829376"},
		Flags: []Flag{flagConsistencia,
			{Nome: "ttl", Curta: "t", Ajuda: "expira depois da duração (30s, 15m, 2h)", Tipo: FlagDuracao},
			{Nome: "se-versao", Ajuda: "só grava se a versão atual for esta", Tipo: FlagInteiro},
			{Nome: "se-ausente", Curta: "n", Ajuda: "só grava se a chave não existir", Tipo: FlagBooleano},
			{Nome: "arquivo", Curta: "f", Ajuda: "lê o valor de um arquivo", Tipo: FlagTexto}},
		Exec: execPut,
	})
	registrar(&Comando{
		Nome: "del", Apelidos: []string{"delete", "apagar", "rm"}, Uso: "<chave>...", Grupo: "Dados", Min: 1, Max: -1,
		Resumo:    "Apaga uma ou mais chaves",
		Descricao: "Grava uma lápide versionada em cada réplica. A lápide é removida do disco na compactação, depois do período de graça.",
		Exemplos:  []string{"leme del usuario:42", "leme del sessao:1 sessao:2 sessao:3"},
		Flags:     []Flag{flagConsistencia, {Nome: "se-versao", Ajuda: "só apaga se a versão atual for esta", Tipo: FlagInteiro}},
		Exec:      execDel,
	})
	registrar(&Comando{
		Nome: "exists", Apelidos: []string{"existe"}, Uso: "<chave>", Grupo: "Dados", Min: 1, Max: 1,
		Resumo:   "Diz se a chave existe (código de saída 0 ou 3)",
		Exemplos: []string{"leme exists usuario:42 && echo existe"},
		Flags:    []Flag{flagConsistencia},
		Exec:     execExists,
	})
	registrar(&Comando{
		Nome: "ttl", Uso: "<chave>", Grupo: "Dados", Min: 1, Max: 1,
		Resumo: "Mostra quanto falta para a chave expirar",
		Flags:  []Flag{flagConsistencia},
		Exec:   execTTL,
	})
	registrar(&Comando{
		Nome: "expire", Apelidos: []string{"expirar"}, Uso: "<chave> <duração | 0>", Grupo: "Dados", Min: 2, Max: 2,
		Resumo:   "Define um novo TTL (0 remove a expiração)",
		Exemplos: []string{"leme expire sessao:9f3 10m", "leme expire sessao:9f3 0"},
		Flags:    []Flag{flagConsistencia},
		Exec:     execExpire,
	})
	registrar(&Comando{
		Nome: "scan", Apelidos: []string{"varrer", "listar"}, Uso: "[prefixo]", Grupo: "Dados", Min: 0, Max: 1,
		Resumo:    "Lista chaves em ordem, com paginação por prefixo",
		Descricao: "Consulta todos os nós vivos, junta as respostas pela versão mais nova e devolve as chaves em ordem lexicográfica. Use --depois com a última chave para a próxima página ou --todas para percorrer tudo.",
		Exemplos:  []string{"leme scan usuario:", "leme scan pedido: --limite 20 --valores", "leme scan pedido: --depois pedido:0020", "leme scan --todas -o texto > chaves.txt"},
		Flags: []Flag{
			{Nome: "limite", Curta: "l", Ajuda: "máximo de chaves por página (padrão 50)", Tipo: FlagInteiro},
			{Nome: "depois", Ajuda: "começa depois desta chave", Tipo: FlagTexto},
			{Nome: "valores", Curta: "v", Ajuda: "inclui os valores", Tipo: FlagBooleano},
			{Nome: "todas", Curta: "a", Ajuda: "percorre todas as páginas", Tipo: FlagBooleano}},
		Exec: execScan,
	})
	registrar(&Comando{
		Nome: "keys", Apelidos: []string{"chaves"}, Uso: "[prefixo]", Grupo: "Dados", Min: 0, Max: 1,
		Resumo: "Lista só os nomes das chaves (uma por linha)",
		Flags:  []Flag{{Nome: "limite", Curta: "l", Ajuda: "máximo de chaves (padrão 1000)", Tipo: FlagInteiro}},
		Exec:   execKeys,
	})
	registrar(&Comando{
		Nome: "count", Apelidos: []string{"contar"}, Uso: "[prefixo]", Grupo: "Dados", Min: 0, Max: 1,
		Resumo: "Conta as chaves com o prefixo",
		Exec:   execCount,
	})
}

func execGet(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	c, err := a.consistencia(s)
	if err != nil {
		return nil, err
	}
	v, err := s.Cliente.Get(ctx, []byte(a.Pos[0]), c)
	if err != nil {
		return nil, err
	}
	dados := map[string]any{"chave": a.Pos[0], "versao": strconv.FormatUint(v.Versao, 10), "coordenador": v.Coordenador, "respostas": v.Respostas}
	if utf8.Valid(v.Valor) {
		dados["valor"] = string(v.Valor)
	} else {
		dados["valor_base64"] = v.Valor
	}
	if v.Expira > 0 {
		dados["expira"] = time.UnixMilli(v.Expira).Format(time.RFC3339)
		dados["ttl_ms"] = v.TTL().Milliseconds()
	}
	r := &saida.Resultado{Bruto: v.Valor, Dados: dados}
	if a.Bool("detalhes") {
		r.Pares = []saida.Par{
			{Rotulo: "chave", Valor: a.Pos[0]},
			{Rotulo: "valor", Valor: saida.Exibivel(v.Valor)},
			{Rotulo: "tamanho", Valor: saida.Bytes(int64(len(v.Valor)))},
			{Rotulo: "versão", Valor: strconv.FormatUint(v.Versao, 10)},
			{Rotulo: "gravado em", Valor: time.UnixMilli(int64(v.Versao >> 16)).Format("02/01/2006 15:04:05.000")},
			{Rotulo: "ttl", Valor: textoTTL(v.Expira)},
			{Rotulo: "réplicas", Valor: fmt.Sprintf("%d responderam", v.Respostas)},
			{Rotulo: "coordenador", Valor: v.Coordenador},
		}
	}
	return r, nil
}

func textoTTL(expira int64) string {
	if expira == 0 {
		return "sem expiração"
	}
	d := time.Until(time.UnixMilli(expira))
	if d <= 0 {
		return "expirada"
	}
	return saida.Duracao(d.Round(time.Second))
}

func lerValor(a Args) ([]byte, error) {
	if arq := a.Texto("arquivo"); arq != "" {
		if len(a.Pos) > 1 {
			return nil, Uso("use o valor ou --arquivo, não os dois")
		}
		return os.ReadFile(arq)
	}
	if len(a.Pos) < 2 {
		return nil, Uso("uso: put <chave> <valor | ->")
	}
	if a.Pos[1] == "-" && a.Entrada != nil {
		return io.ReadAll(io.LimitReader(a.Entrada, 32<<20+1))
	}
	return []byte(a.Pos[1]), nil
}

func resumoEscrita(e cliente.Escrita, s *Sessao) string {
	return fmt.Sprintf("%s  %s", s.Estilo.Verde("OK"), s.Estilo.Fraco(fmt.Sprintf("versão %d · %d/%d réplicas confirmaram (mínimo %d) · via %s", e.Versao, e.Confirmacoes, e.Replicas, e.Necessarias, e.Coordenador)))
}

func dadosEscrita(chave string, e cliente.Escrita) map[string]any {
	return map[string]any{"chave": chave, "versao": strconv.FormatUint(e.Versao, 10), "confirmacoes": e.Confirmacoes, "necessarias": e.Necessarias, "replicas": e.Replicas, "coordenador": e.Coordenador}
}

func execPut(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	c, err := a.consistencia(s)
	if err != nil {
		return nil, err
	}
	valor, err := lerValor(a)
	if err != nil {
		return nil, err
	}
	ttl, err := a.Duracao("ttl")
	if err != nil {
		return nil, err
	}
	sv, err := a.Uint("se-versao")
	if err != nil {
		return nil, err
	}
	e, err := s.Cliente.Put(ctx, []byte(a.Pos[0]), valor, cliente.OpcoesPut{TTL: ttl, Consistencia: c, SeVersao: sv, SeAusente: a.Bool("se-ausente")})
	if err != nil {
		return nil, err
	}
	return &saida.Resultado{Mensagem: resumoEscrita(e, s), Texto: "OK", Dados: dadosEscrita(a.Pos[0], e)}, nil
}

func execDel(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	c, err := a.consistencia(s)
	if err != nil {
		return nil, err
	}
	sv, err := a.Uint("se-versao")
	if err != nil {
		return nil, err
	}
	if sv != 0 && len(a.Pos) > 1 {
		return nil, Uso("--se-versao funciona com uma chave por vez")
	}
	var lista []map[string]any
	var ultima cliente.Escrita
	for _, k := range a.Pos {
		e, err := s.Cliente.Del(ctx, []byte(k), c, sv)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		ultima = e
		lista = append(lista, dadosEscrita(k, e))
	}
	r := &saida.Resultado{Texto: "OK", Dados: lista}
	if len(a.Pos) == 1 {
		r.Mensagem = resumoEscrita(ultima, s)
		r.Dados = lista[0]
	} else {
		r.Mensagem = fmt.Sprintf("%s  %s", s.Estilo.Verde("OK"), s.Estilo.Fraco(fmt.Sprintf("%d chaves apagadas", len(a.Pos))))
	}
	return r, nil
}

func execExists(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	c, err := a.consistencia(s)
	if err != nil {
		return nil, err
	}
	_, err = s.Cliente.TTL(ctx, []byte(a.Pos[0]), c)
	if protocolo.CodigoDe(err) == protocolo.CodigoNaoEncontrado {
		return &saida.Resultado{Mensagem: "não", Texto: "0", Dados: map[string]any{"chave": a.Pos[0], "existe": false}}, &ErroSaida{Codigo: SaidaNaoEncontrado}
	}
	if err != nil {
		return nil, err
	}
	return &saida.Resultado{Mensagem: "sim", Texto: "1", Dados: map[string]any{"chave": a.Pos[0], "existe": true}}, nil
}

func execTTL(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	c, err := a.consistencia(s)
	if err != nil {
		return nil, err
	}
	v, err := s.Cliente.TTL(ctx, []byte(a.Pos[0]), c)
	if err != nil {
		return nil, err
	}
	ms := int64(-1)
	if v.Expira > 0 {
		ms = v.TTL().Milliseconds()
	}
	return &saida.Resultado{Mensagem: textoTTL(v.Expira), Texto: strconv.FormatInt(ms, 10), Dados: map[string]any{"chave": a.Pos[0], "ttl_ms": ms, "expira": v.Expira}}, nil
}

func execExpire(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	c, err := a.consistencia(s)
	if err != nil {
		return nil, err
	}
	d, err := ParseDuracao(a.Pos[1])
	if err != nil {
		return nil, err
	}
	e, err := s.Cliente.Expirar(ctx, []byte(a.Pos[0]), d, c)
	if err != nil {
		return nil, err
	}
	return &saida.Resultado{Mensagem: resumoEscrita(e, s), Texto: "OK", Dados: dadosEscrita(a.Pos[0], e)}, nil
}

func prefixo(a Args) string {
	if len(a.Pos) > 0 {
		return a.Pos[0]
	}
	return ""
}

func execScan(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	limite, err := a.Inteiro("limite", 50)
	if err != nil {
		return nil, err
	}
	if limite <= 0 || limite > 10000 {
		return nil, Uso("--limite deve ficar entre 1 e 10000")
	}
	valores := a.Bool("valores")
	todas := a.Bool("todas")
	depois := a.Texto("depois")
	var itens []protocolo.Item
	mais := false
	for {
		pag, m, err := s.Cliente.Scan(ctx, cliente.OpcoesScan{Prefixo: prefixo(a), Depois: depois, Limite: limite, SoChaves: !valores})
		if err != nil {
			return nil, err
		}
		itens = append(itens, pag...)
		mais = m
		if !todas || !m || len(pag) == 0 {
			break
		}
		depois = string(pag[len(pag)-1].Chave)
	}
	g := &saida.Grade{Colunas: []string{"CHAVE", "VERSÃO", "TTL"}, Direita: []int{1}}
	if valores {
		g.Colunas = append(g.Colunas, "VALOR")
	}
	var dados []map[string]any
	var texto []byte
	for _, it := range itens {
		l := []string{string(it.Chave), strconv.FormatUint(it.Versao, 10), textoTTL(it.Expira)}
		d := map[string]any{"chave": string(it.Chave), "versao": strconv.FormatUint(it.Versao, 10)}
		if it.Expira > 0 {
			d["expira"] = it.Expira
		}
		texto = append(texto, it.Chave...)
		if valores {
			l = append(l, saida.Resumir(it.Valor, 60))
			d["valor"] = saida.Exibivel(it.Valor)
			texto = append(texto, '\t')
			texto = append(texto, it.Valor...)
		}
		texto = append(texto, '\n')
		g.Linhas = append(g.Linhas, l)
		dados = append(dados, d)
	}
	r := &saida.Resultado{Grade: g, Bruto: texto, Dados: map[string]any{"itens": dados, "mais": mais}}
	r.Rodape = fmt.Sprintf("%d chaves", len(itens))
	if mais && len(itens) > 0 {
		ult := string(itens[len(itens)-1].Chave)
		r.Rodape += fmt.Sprintf(" · há mais: --depois %s", ult)
		r.Dados.(map[string]any)["proxima"] = ult
	}
	return r, nil
}

func execKeys(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	limite, err := a.Inteiro("limite", 1000)
	if err != nil {
		return nil, err
	}
	itens, mais, err := s.Cliente.Scan(ctx, cliente.OpcoesScan{Prefixo: prefixo(a), Limite: limite, SoChaves: true})
	if err != nil {
		return nil, err
	}
	var nomes []string
	var b []byte
	for _, it := range itens {
		nomes = append(nomes, string(it.Chave))
		b = append(append(b, it.Chave...), '\n')
	}
	r := &saida.Resultado{Bruto: b, Dados: map[string]any{"chaves": nomes, "mais": mais}}
	if len(nomes) == 0 {
		r.Bruto = nil
		r.Mensagem = s.Estilo.Fraco("(nenhuma chave)")
	} else {
		r.Bruto = b[:len(b)-1]
	}
	if mais {
		r.Rodape = fmt.Sprintf("mostrando %d · use --limite para ver mais", len(nomes))
	}
	return r, nil
}

func execCount(ctx context.Context, s *Sessao, a Args) (*saida.Resultado, error) {
	n, err := s.Cliente.Contar(ctx, prefixo(a))
	if err != nil {
		return nil, err
	}
	return &saida.Resultado{Mensagem: strconv.Itoa(n), Texto: strconv.Itoa(n), Dados: map[string]any{"prefixo": prefixo(a), "chaves": n}}, nil
}
