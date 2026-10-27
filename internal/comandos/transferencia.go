package comandos

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/cliente"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/protocolo"
)

// ItemExportado é uma chave no formato de exportação. Valores que não são
// UTF-8 válido vão em base64, e o TTL é gravado como tempo restante para que
// a importação recrie a expiração relativa ao momento em que roda.
type ItemExportado struct {
	Chave       string `json:"chave"`
	Valor       string `json:"valor"`
	Codificacao string `json:"codificacao"`
	TTLms       int64  `json:"ttl_ms,omitempty"`
	Versao      uint64 `json:"versao,omitempty"`
}

// Formatos aceitos por Exportar e Importar.
var FormatosTransferencia = []string{"ndjson", "json", "csv"}

var cabecalhoCSV = []string{"chave", "valor", "codificacao", "ttl_ms", "versao"}

func paraExportacao(it protocolo.Item, agora int64) (ItemExportado, bool) {
	e := ItemExportado{Chave: string(it.Chave), Versao: it.Versao, Codificacao: "texto"}
	if it.Expira > 0 {
		e.TTLms = it.Expira - agora
		if e.TTLms <= 0 {
			return e, false
		}
	}
	if utf8.Valid(it.Valor) {
		e.Valor = string(it.Valor)
	} else {
		e.Valor = base64.StdEncoding.EncodeToString(it.Valor)
		e.Codificacao = "base64"
	}
	return e, true
}

// FormatoPorNome deduz o formato pela extensão do arquivo ("" quando não sabe).
func FormatoPorNome(nome string) string {
	n := strings.ToLower(nome)
	switch {
	case strings.HasSuffix(n, ".csv"):
		return "csv"
	case strings.HasSuffix(n, ".ndjson"), strings.HasSuffix(n, ".jsonl"):
		return "ndjson"
	case strings.HasSuffix(n, ".json"):
		return "json"
	}
	return ""
}

func validarFormato(f string) (string, error) {
	f = strings.ToLower(strings.TrimSpace(f))
	if f == "" {
		return "ndjson", nil
	}
	if !contem(FormatosTransferencia, f) {
		return "", Uso("formato %q inválido: use ndjson, json ou csv", f)
	}
	return f, nil
}

// Exportar percorre todas as chaves do prefixo, página por página, e escreve
// cada uma no formato pedido. Devolve quantas chaves foram escritas.
func Exportar(ctx context.Context, s *Sessao, prefixo, formato string, w io.Writer) (int, error) {
	formato, err := validarFormato(formato)
	if err != nil {
		return 0, err
	}
	bw := bufio.NewWriterSize(w, 64<<10)
	defer bw.Flush()
	var cw *csv.Writer
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)
	switch formato {
	case "csv":
		cw = csv.NewWriter(bw)
		cw.Write(cabecalhoCSV)
	case "json":
		bw.WriteString("[\n")
	}
	total := 0
	depois := ""
	for {
		pag, mais, err := s.Cliente.Scan(ctx, cliente.OpcoesScan{Prefixo: prefixo, Depois: depois, Limite: 500})
		if err != nil {
			return total, err
		}
		agora := time.Now().UnixMilli()
		for _, it := range pag {
			e, ok := paraExportacao(it, agora)
			if !ok {
				continue
			}
			switch formato {
			case "csv":
				ttl := ""
				if e.TTLms > 0 {
					ttl = strconv.FormatInt(e.TTLms, 10)
				}
				cw.Write([]string{e.Chave, e.Valor, e.Codificacao, ttl, strconv.FormatUint(e.Versao, 10)})
			case "json":
				if total > 0 {
					bw.WriteString(",\n")
				}
				b, _ := json.Marshal(e)
				bw.WriteString("  ")
				bw.Write(b)
			default:
				if err := enc.Encode(e); err != nil {
					return total, err
				}
			}
			total++
		}
		if !mais || len(pag) == 0 {
			break
		}
		depois = string(pag[len(pag)-1].Chave)
	}
	switch formato {
	case "csv":
		cw.Flush()
		if err := cw.Error(); err != nil {
			return total, err
		}
	case "json":
		if total > 0 {
			bw.WriteString("\n")
		}
		bw.WriteString("]\n")
	}
	return total, bw.Flush()
}

// OpcoesImportar controla Importar. Formato vazio detecta pelo conteúdo.
type OpcoesImportar struct {
	Formato   string
	SeAusente bool
	Simular   bool
	Prefixo   string
}

// RelatorioImportacao resume o que aconteceu em uma importação.
type RelatorioImportacao struct {
	Lidos     int      `json:"lidos"`
	Gravados  int      `json:"gravados"`
	Ignorados int      `json:"ignorados"`
	Falhas    int      `json:"falhas"`
	Simulado  bool     `json:"simulado,omitempty"`
	Erros     []string `json:"erros,omitempty"`
}

const maxErrosRelatorio = 20

func (r *RelatorioImportacao) erro(formato string, args ...any) {
	r.Falhas++
	if len(r.Erros) < maxErrosRelatorio {
		r.Erros = append(r.Erros, fmt.Sprintf(formato, args...))
	}
}

func detectarFormato(br *bufio.Reader) string {
	for {
		b, err := br.Peek(1)
		if err != nil || len(b) == 0 {
			return "ndjson"
		}
		switch b[0] {
		case ' ', '\t', '\r', '\n', 0xEF, 0xBB, 0xBF:
			br.ReadByte()
			continue
		case '[':
			return "json"
		case '{':
			return "ndjson"
		}
		return "csv"
	}
}

func lerItens(r io.Reader, formato string, rel *RelatorioImportacao, emitir func(n int, e ItemExportado)) error {
	br := bufio.NewReaderSize(r, 64<<10)
	if formato == "" {
		formato = detectarFormato(br)
	}
	formato, err := validarFormato(formato)
	if err != nil {
		return err
	}
	switch formato {
	case "json":
		dec := json.NewDecoder(br)
		tok, err := dec.Token()
		if err != nil {
			return Uso("JSON inválido: %v", err)
		}
		if d, ok := tok.(json.Delim); !ok || d != '[' {
			return Uso("o arquivo JSON precisa ser uma lista de objetos")
		}
		n := 0
		for dec.More() {
			n++
			var e ItemExportado
			if err := dec.Decode(&e); err != nil {
				return Uso("item %d: %v", n, err)
			}
			emitir(n, e)
		}
	case "csv":
		cr := csv.NewReader(br)
		cr.FieldsPerRecord = -1
		cab, err := cr.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return Uso("CSV inválido: %v", err)
		}
		col := map[string]int{}
		for i, c := range cab {
			col[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(c, "\ufeff")))] = i
		}
		ic, ok1 := col["chave"]
		iv, ok2 := col["valor"]
		if !ok1 || !ok2 {
			return Uso("o CSV precisa das colunas chave e valor")
		}
		campo := func(l []string, nome string) string {
			if i, ok := col[nome]; ok && i < len(l) {
				return l[i]
			}
			return ""
		}
		n := 1
		for {
			l, err := cr.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			n++
			if err != nil {
				rel.Lidos++
				rel.erro("linha %d: %v", n, err)
				continue
			}
			if ic >= len(l) || iv >= len(l) {
				rel.Lidos++
				rel.erro("linha %d: faltam colunas", n)
				continue
			}
			e := ItemExportado{Chave: l[ic], Valor: l[iv], Codificacao: campo(l, "codificacao")}
			if t := campo(l, "ttl_ms"); t != "" {
				e.TTLms, _ = strconv.ParseInt(t, 10, 64)
			}
			emitir(n, e)
		}
	default:
		n := 0
		for {
			linha, err := br.ReadBytes('\n')
			if len(bytes.TrimSpace(linha)) > 0 {
				n++
				var e ItemExportado
				if jerr := json.Unmarshal(linha, &e); jerr != nil {
					rel.Lidos++
					rel.erro("linha %d: %v", n, jerr)
				} else {
					emitir(n, e)
				}
			} else if len(linha) > 0 {
				n++
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return err
			}
		}
	}
	return nil
}

// Importar lê chaves no formato de exportação e grava no cluster com até 16
// escritas em paralelo. Com SeAusente, chaves que já existem são ignoradas;
// com Simular, nada é gravado e o relatório diz o que seria feito.
func Importar(ctx context.Context, s *Sessao, r io.Reader, op OpcoesImportar) (RelatorioImportacao, error) {
	rel := RelatorioImportacao{Simulado: op.Simular}
	type tarefa struct {
		n     int
		chave string
		valor []byte
		ttl   time.Duration
	}
	fila := make(chan tarefa, 64)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range fila {
				if ctx.Err() != nil {
					continue
				}
				_, err := s.Cliente.Put(ctx, []byte(t.chave), t.valor, cliente.OpcoesPut{TTL: t.ttl, Consistencia: s.Consistencia, SeAusente: op.SeAusente})
				mu.Lock()
				switch {
				case err == nil:
					rel.Gravados++
				case op.SeAusente && CodigoSaida(err) == SaidaConflito:
					rel.Ignorados++
				default:
					rel.erro("%s: %v", t.chave, err)
				}
				mu.Unlock()
			}
		}()
	}
	errLeitura := lerItens(r, op.Formato, &rel, func(n int, e ItemExportado) {
		mu.Lock()
		rel.Lidos++
		mu.Unlock()
		if e.Chave == "" {
			mu.Lock()
			rel.erro("item %d: chave vazia", n)
			mu.Unlock()
			return
		}
		valor := []byte(e.Valor)
		if strings.EqualFold(e.Codificacao, "base64") {
			b, err := base64.StdEncoding.DecodeString(e.Valor)
			if err != nil {
				mu.Lock()
				rel.erro("%s: base64 inválido", e.Chave)
				mu.Unlock()
				return
			}
			valor = b
		}
		var ttl time.Duration
		if e.TTLms > 0 {
			ttl = time.Duration(e.TTLms) * time.Millisecond
		}
		if op.Simular {
			mu.Lock()
			rel.Gravados++
			mu.Unlock()
			return
		}
		select {
		case fila <- tarefa{n: n, chave: op.Prefixo + e.Chave, valor: valor, ttl: ttl}:
		case <-ctx.Done():
		}
	})
	close(fila)
	wg.Wait()
	if errLeitura != nil {
		return rel, errLeitura
	}
	return rel, ctx.Err()
}
