package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestExportarEImportar(t *testing.T) {
	a := novoAmbiente(t)
	a.rodar("", "put", "o:usuario:1", `{"nome":"Ana"}`)
	a.rodar("", "put", "o:usuario:2", "texto, com vírgula e \"aspas\"")
	a.rodar("", "put", "o:sessao:1", "ativa", "--ttl", "1h")
	a.rodar("\x00\x01\xffbinário", "put", "o:arquivo:1", "-")

	for _, formato := range []string{"ndjson", "json", "csv"} {
		arq := filepath.Join(t.TempDir(), "backup."+formato)
		if _, e, cod := a.rodar("", "export", "o:", "--arquivo", arq); cod != 0 {
			t.Fatalf("export %s: %d %s", formato, cod, e)
		}
		destino := formato + ":"
		out, e, cod := a.rodar("", "import", arq, "--prefixo", destino, "-o", "json")
		var rel map[string]any
		if cod != 0 || json.Unmarshal([]byte(out), &rel) != nil || rel["gravados"] != float64(4) {
			t.Fatalf("import %s: %d %s %s", formato, cod, out, e)
		}
		for _, k := range []string{"o:usuario:1", "o:usuario:2", "o:sessao:1"} {
			orig, _, _ := a.rodar("", "get", k)
			copia, _, _ := a.rodar("", "get", destino+k)
			if orig != copia {
				t.Fatalf("%s %s: %q != %q", formato, k, copia, orig)
			}
		}
		bin, _, _ := a.rodar("", "get", destino+"o:arquivo:1", "-o", "texto")
		if bin != "\x00\x01\xffbinário\n" {
			t.Fatalf("%s valor binário: %q", formato, bin)
		}
		ttl, _, _ := a.rodar("", "ttl", destino+"o:sessao:1")
		if strings.Contains(ttl, "sem expiração") {
			t.Fatalf("%s perdeu o TTL", formato)
		}
	}

	arq := filepath.Join(t.TempDir(), "backup.ndjson")
	a.rodar("", "export", "o:usuario:", "--arquivo", arq)
	out, _, cod := a.rodar("", "import", arq, "--se-ausente", "-o", "json")
	if cod != 0 || !strings.Contains(out, `"ignorados": 2`) {
		t.Fatalf("--se-ausente deveria ignorar as existentes: %d %s", cod, out)
	}
	out, _, cod = a.rodar("", "import", arq, "--prefixo", "sim:", "--simular", "-o", "json")
	if cod != 0 || !strings.Contains(out, `"simulado": true`) {
		t.Fatalf("simulação: %d %s", cod, out)
	}
	if _, _, cod = a.rodar("", "exists", "sim:o:usuario:1"); cod != 3 {
		t.Fatal("--simular não deveria gravar")
	}
	ruim := filepath.Join(t.TempDir(), "ruim.ndjson")
	os.WriteFile(ruim, []byte("{\"chave\":\"ok:1\",\"valor\":\"a\"}\nisso não é json\n"), 0o600)
	if _, _, cod = a.rodar("", "import", ruim); cod != 1 {
		t.Fatalf("linha inválida deveria sair com 1, saiu com %d", cod)
	}
	if _, _, cod = a.rodar("", "exists", "ok:1"); cod != 0 {
		t.Fatal("as linhas válidas deveriam ser importadas")
	}
	in, _, cod := a.rodar("", "export", "o:usuario:")
	if cod != 0 || strings.Count(in, "\n") != 2 {
		t.Fatalf("export na saída padrão: %q", in)
	}
	if _, e, cod := a.rodar(in, "import", "-", "--prefixo", "pipe:"); cod != 0 {
		t.Fatalf("import da entrada padrão: %d %s", cod, e)
	}
}

func TestIncrConcorrente(t *testing.T) {
	a := novoAmbiente(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if _, e, cod := a.rodar("", "incr", "contador"); cod != 0 {
					t.Errorf("incr: %d %s", cod, e)
				}
			}
		}()
	}
	wg.Wait()
	out, _, _ := a.rodar("", "get", "contador")
	if n, _ := strconv.Atoi(strings.TrimSpace(out)); n != 40 {
		t.Fatalf("40 incrementos concorrentes deram %q", out)
	}
	out, _, _ = a.rodar("", "incr", "contador", "-5")
	if strings.TrimSpace(out) != "35" {
		t.Fatalf("incr com delta negativo: %q", out)
	}
	out, _, _ = a.rodar("", "decr", "contador", "10")
	if strings.TrimSpace(out) != "25" {
		t.Fatalf("decr: %q", out)
	}
	a.rodar("", "put", "nome", "Ana")
	if _, _, cod := a.rodar("", "incr", "nome"); cod != 2 {
		t.Fatalf("incr em texto deveria sair com 2, saiu com %d", cod)
	}
}

func TestCopiarRenomearEVarias(t *testing.T) {
	a := novoAmbiente(t)
	a.rodar("", "put", "a", "1", "--ttl", "1h")
	if _, e, cod := a.rodar("", "copy", "a", "b"); cod != 0 {
		t.Fatalf("copy: %d %s", cod, e)
	}
	if _, _, cod := a.rodar("", "copy", "a", "b"); cod != 6 {
		t.Fatalf("copy sobre destino existente deveria sair com 6, saiu com %d", cod)
	}
	if _, _, cod := a.rodar("", "copy", "a", "b", "--substituir"); cod != 0 {
		t.Fatal("copy --substituir falhou")
	}
	ttl, _, _ := a.rodar("", "ttl", "b")
	if strings.Contains(ttl, "sem expiração") {
		t.Fatal("copy perdeu o TTL")
	}
	if _, e, cod := a.rodar("", "rename", "b", "c"); cod != 0 {
		t.Fatalf("rename: %d %s", cod, e)
	}
	if _, _, cod := a.rodar("", "exists", "b"); cod != 3 {
		t.Fatal("rename deveria apagar a origem")
	}
	if _, e, cod := a.rodar("", "mset", "x=1", "y=dois", "z=3=4"); cod != 0 {
		t.Fatalf("mset: %d %s", cod, e)
	}
	out, _, cod := a.rodar("", "mget", "x", "y", "z", "nada", "-o", "json")
	var itens []map[string]any
	if cod != 0 || json.Unmarshal([]byte(out), &itens) != nil || len(itens) != 4 || itens[2]["valor"] != "3=4" || itens[3]["ausente"] != true {
		t.Fatalf("mget: %d %s", cod, out)
	}
	if _, _, cod := a.rodar("", "mset", "semigual"); cod != 2 {
		t.Fatalf("mset sem = deveria sair com 2, saiu com %d", cod)
	}
}

func TestDiagnosticoEPainel(t *testing.T) {
	a := novoAmbiente(t)
	out, e, cod := a.rodar("", "doctor", "-o", "json")
	var vs []map[string]any
	if cod != 0 || json.Unmarshal([]byte(out), &vs) != nil || len(vs) < 8 {
		t.Fatalf("doctor: %d %s %s", cod, out, e)
	}
	for _, v := range vs {
		if v["estado"] == "falha" {
			t.Fatalf("cluster saudável não deveria ter falhas: %v", v)
		}
	}
	if _, _, cod = a.rodar("", "--token", "errado", "doctor"); cod != 7 {
		t.Fatalf("token errado deveria sair com 7, saiu com %d", cod)
	}
	out, e, cod = a.rodar("", "top", "--vezes", "2", "--intervalo", "200ms")
	if cod != 0 || strings.Count(out, "no-") < 3 || !strings.Contains(out, "OPS/S") {
		t.Fatalf("top: %d %s %s", cod, out, e)
	}
	a.rodar("", "put", "w:1", "a")
	out, e, cod = a.rodar("", "watch", "--prefixo", "w:", "--vezes", "1", "-o", "json")
	if cod != 0 {
		t.Fatalf("watch: %d %s %s", cod, out, e)
	}
	if _, _, cod = a.rodar("", "watch"); cod != 2 {
		t.Fatalf("watch sem alvo deveria sair com 2, saiu com %d", cod)
	}
}
