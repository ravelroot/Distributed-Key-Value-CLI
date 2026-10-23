package comandos

import (
	"errors"
	"reflect"
	"testing"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/protocolo"
)

func TestTokenizar(t *testing.T) {
	casos := map[string][]string{
		`put usuario:1 Ana`:             {"put", "usuario:1", "Ana"},
		`put nota "texto com  espaços"`: {"put", "nota", "texto com  espaços"},
		`put json '{"a": 1}'`:           {"put", "json", `{"a": 1}`},
		`put k linha\nnova`:             {"put", "k", "linha\nnova"},
		`put k "aspas \"dentro\""`:      {"put", "k", `aspas "dentro"`},
		`   get    k   `:                {"get", "k"},
		`put k ''`:                      {"put", "k", ""},
	}
	for entrada, esperado := range casos {
		got, err := Tokenizar(entrada)
		if err != nil || !reflect.DeepEqual(got, esperado) {
			t.Errorf("%q → %q (%v), esperado %q", entrada, got, err, esperado)
		}
	}
	if _, err := Tokenizar(`put k "sem fim`); err == nil {
		t.Error("aspas abertas deveriam falhar")
	}
}

func TestBuscarEAnalisar(t *testing.T) {
	c, resto := Buscar([]string{"cluster", "nos"})
	if c == nil || c.Caminho() != "cluster nodes" || len(resto) != 0 {
		t.Fatalf("apelido de subcomando: %v %v", c, resto)
	}
	c, resto = Buscar([]string{"gravar", "k", "v", "--ttl", "30s", "-n", "--consistencia=todos"})
	if c == nil || c.Nome != "put" {
		t.Fatalf("apelido de comando: %v", c)
	}
	a, err := Analisar(c, resto)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Pos, []string{"k", "v"}) || a.Texto("ttl") != "30s" || !a.Bool("se-ausente") || a.Texto("consistencia") != "todos" {
		t.Fatalf("args: %+v", a)
	}
	if d, err := a.Duracao("ttl"); err != nil || d.Seconds() != 30 {
		t.Fatalf("duração: %v %v", d, err)
	}
	a, err = Analisar(c, []string{"saldo", "-5"})
	if err != nil || a.Pos[1] != "-5" {
		t.Fatalf("número negativo como valor: %+v %v", a, err)
	}
	if _, err := Analisar(c, []string{"k", "--inexistente"}); err == nil {
		t.Fatal("opção desconhecida deveria falhar")
	}
	if _, err := Analisar(c, []string{"k", "--ttl"}); err == nil {
		t.Fatal("opção sem valor deveria falhar")
	}
	if err := Validar(c, Args{}); err == nil {
		t.Fatal("put sem chave deveria falhar")
	}
}

func TestCodigosDeSaida(t *testing.T) {
	casos := map[error]int{
		nil: SaidaOK,
		protocolo.NovoErro(protocolo.CodigoNaoEncontrado, "x"): SaidaNaoEncontrado,
		protocolo.NovoErro(protocolo.CodigoIndisponivel, "x"):  SaidaIndisponivel,
		protocolo.NovoErro(protocolo.CodigoQuorum, "x"):        SaidaQuorum,
		protocolo.NovoErro(protocolo.CodigoConflito, "x"):      SaidaConflito,
		protocolo.NovoErro(protocolo.CodigoNaoAutorizado, "x"): SaidaAutenticacao,
		Uso("x"):               SaidaUso,
		errors.New("qualquer"): SaidaErro,
	}
	for err, esperado := range casos {
		if got := CodigoSaida(err); got != esperado {
			t.Errorf("%v → %d, esperado %d", err, got, esperado)
		}
	}
}

func TestRegistroSemDuplicados(t *testing.T) {
	vistos := map[string]string{}
	for _, c := range Registro {
		for _, n := range append([]string{c.Nome}, c.Apelidos...) {
			k := c.Pai + " " + n
			if outro, ok := vistos[k]; ok {
				t.Fatalf("%q usado por %s e %s", n, outro, c.Caminho())
			}
			vistos[k] = c.Caminho()
		}
		if c.Resumo == "" || c.Grupo == "" {
			t.Fatalf("%s sem resumo ou grupo", c.Caminho())
		}
	}
}
