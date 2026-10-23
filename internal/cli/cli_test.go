package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/cli"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/teste"
)

type ambiente struct {
	t     *testing.T
	nos   string
	token string
}

func novoAmbiente(t *testing.T) *ambiente {
	c := teste.NovoCluster(t, 3, "segredo-de-teste")
	t.Setenv("LEME_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("NO_COLOR", "1")
	return &ambiente{t: t, nos: strings.Join(c.Enderecos(), ","), token: c.Token}
}

func (a *ambiente) rodar(entrada string, args ...string) (string, string, int) {
	a.t.Helper()
	arq := filepath.Join(a.t.TempDir(), "entrada")
	os.WriteFile(arq, []byte(entrada), 0o600)
	f, _ := os.Open(arq)
	defer f.Close()
	var out, errb bytes.Buffer
	cod := cli.Executar(append([]string{"--nos", a.nos, "--token", a.token}, args...), f, &out, &errb)
	return out.String(), errb.String(), cod
}

func TestComandosDiretos(t *testing.T) {
	a := novoAmbiente(t)
	if _, e, cod := a.rodar("", "put", "usuario:1", `{"nome":"Ana"}`); cod != 0 {
		t.Fatalf("put: %d %s", cod, e)
	}
	out, _, cod := a.rodar("", "get", "usuario:1")
	if cod != 0 || strings.TrimSpace(out) != `{"nome":"Ana"}` {
		t.Fatalf("get: %d %q", cod, out)
	}
	out, _, cod = a.rodar("", "get", "usuario:1", "-o", "json")
	var v map[string]any
	if cod != 0 || json.Unmarshal([]byte(out), &v) != nil || v["valor"] != `{"nome":"Ana"}` {
		t.Fatalf("get json: %d %s", cod, out)
	}
	if _, _, cod = a.rodar("", "get", "nao-existe"); cod != 3 {
		t.Fatalf("chave ausente deveria sair com 3, saiu com %d", cod)
	}
	if _, _, cod = a.rodar("", "exists", "usuario:1"); cod != 0 {
		t.Fatalf("exists: %d", cod)
	}
	if _, _, cod = a.rodar("", "put"); cod != 2 {
		t.Fatalf("uso errado deveria sair com 2, saiu com %d", cod)
	}
	if _, _, cod = a.rodar("valor vindo da entrada", "put", "arquivo:1", "-"); cod != 0 {
		t.Fatal("put com entrada padrão falhou")
	}
	out, _, _ = a.rodar("", "get", "arquivo:1")
	if strings.TrimSpace(out) != "valor vindo da entrada" {
		t.Fatalf("valor lido da entrada: %q", out)
	}
	out, _, cod = a.rodar("", "cluster", "status", "-o", "json")
	if cod != 0 || !strings.Contains(out, `"saude": "saudável"`) {
		t.Fatalf("cluster status: %d %s", cod, out)
	}
	out, _, cod = a.rodar("", "keys", "usuario:", "-o", "texto")
	if cod != 0 || strings.TrimSpace(out) != "usuario:1" {
		t.Fatalf("keys: %d %q", cod, out)
	}
}

func TestTokenErradoSaiCom7(t *testing.T) {
	a := novoAmbiente(t)
	a.token = "errado"
	if _, _, cod := a.rodar("", "get", "x"); cod != 7 {
		t.Fatalf("esperava código 7, veio %d", cod)
	}
}

func TestModoNaoInterativo(t *testing.T) {
	a := novoAmbiente(t)
	script := "# carga inicial\nput pedido:1 \"primeiro pedido\"\nput pedido:2 segundo\n\\saida json\ncount pedido:\nget pedido:1\n"
	out, e, cod := a.rodar(script, "shell")
	if cod != 0 {
		t.Fatalf("shell: %d %s", cod, e)
	}
	if !strings.Contains(out, `"chaves": 2`) || !strings.Contains(out, `"valor": "primeiro pedido"`) {
		t.Fatalf("saída do script: %s", out)
	}
	_, _, cod = a.rodar("get nada\nput depois 1\n", "shell", "--parar-no-erro")
	if cod != 3 {
		t.Fatalf("--parar-no-erro deveria sair com 3, saiu com %d", cod)
	}
	out, _, _ = a.rodar("", "get", "depois")
	if strings.TrimSpace(out) == "1" {
		t.Fatal("o script deveria ter parado antes do segundo comando")
	}
}

func TestPerfis(t *testing.T) {
	a := novoAmbiente(t)
	var out, errb bytes.Buffer
	cod := cli.Executar([]string{"config", "set", "teste", "--nos", a.nos, "--token", a.token, "--consistencia", "todos"}, os.Stdin, &out, &errb)
	if cod != 0 {
		t.Fatalf("config set: %s", errb.String())
	}
	out.Reset()
	cod = cli.Executar([]string{"put", "via-perfil", "ok"}, os.Stdin, &out, &errb)
	if cod != 0 || !strings.Contains(out.String(), "3/3") {
		t.Fatalf("o perfil com consistência todos deveria confirmar 3/3: %d %s %s", cod, out.String(), errb.String())
	}
}
