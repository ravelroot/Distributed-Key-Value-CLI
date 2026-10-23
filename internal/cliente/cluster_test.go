package cliente_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/cliente"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/no"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/protocolo"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/teste"
)

func TestClusterEscritaLeituraEQuorum(t *testing.T) {
	c := teste.NovoCluster(t, 3, "")
	cli := c.Cliente(t)
	ctx := context.Background()

	if _, err := cli.Put(ctx, []byte("usuario:1"), []byte("Ana"), cliente.OpcoesPut{}); err != nil {
		t.Fatalf("put: %v", err)
	}
	v, err := cli.Get(ctx, []byte("usuario:1"), protocolo.ConsistenciaTodos)
	if err != nil || string(v.Valor) != "Ana" {
		t.Fatalf("get: %q %v", v.Valor, err)
	}
	for i := 0; i < 50; i++ {
		k := fmt.Sprintf("pedido:%03d", i)
		if _, err := cli.Put(ctx, []byte(k), []byte(k), cliente.OpcoesPut{}); err != nil {
			t.Fatalf("put %s: %v", k, err)
		}
	}
	itens, mais, err := cli.Scan(ctx, cliente.OpcoesScan{Prefixo: "pedido:", Limite: 20})
	if err != nil || len(itens) != 20 || !mais || string(itens[0].Chave) != "pedido:000" {
		t.Fatalf("scan: %d %v %v", len(itens), mais, err)
	}
	total, err := cli.Contar(ctx, "pedido:")
	if err != nil || total != 50 {
		t.Fatalf("contar: %d %v", total, err)
	}

	c.Nos[2].Derrubar()
	if _, err := cli.Put(ctx, []byte("usuario:2"), []byte("Bia"), cliente.OpcoesPut{Consistencia: protocolo.ConsistenciaQuorum}); err != nil {
		t.Fatalf("put com um nó fora: %v", err)
	}
	if _, err := cli.Get(ctx, []byte("usuario:2"), protocolo.ConsistenciaQuorum); err != nil {
		t.Fatalf("get com um nó fora: %v", err)
	}
	_, err = cli.Put(ctx, []byte("usuario:3"), []byte("Caio"), cliente.OpcoesPut{Consistencia: protocolo.ConsistenciaTodos})
	if protocolo.CodigoDe(err) != protocolo.CodigoQuorum {
		t.Fatalf("esperava falha de quórum, veio %v", err)
	}
}

func TestCondicoesETTL(t *testing.T) {
	c := teste.NovoCluster(t, 3, "segredo")
	cli := c.Cliente(t)
	ctx := context.Background()
	e, err := cli.Put(ctx, []byte("estoque"), []byte("10"), cliente.OpcoesPut{SeAusente: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Put(ctx, []byte("estoque"), []byte("11"), cliente.OpcoesPut{SeAusente: true}); protocolo.CodigoDe(err) != protocolo.CodigoConflito {
		t.Fatalf("esperava conflito, veio %v", err)
	}
	if _, err := cli.Put(ctx, []byte("estoque"), []byte("9"), cliente.OpcoesPut{SeVersao: e.Versao}); err != nil {
		t.Fatalf("cas: %v", err)
	}
	if _, err := cli.Put(ctx, []byte("estoque"), []byte("8"), cliente.OpcoesPut{SeVersao: e.Versao}); protocolo.CodigoDe(err) != protocolo.CodigoConflito {
		t.Fatalf("esperava conflito de versão, veio %v", err)
	}
	if _, err := cli.Put(ctx, []byte("sessao"), []byte("x"), cliente.OpcoesPut{TTL: 150 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if _, err := cli.Get(ctx, []byte("sessao"), 0); protocolo.CodigoDe(err) != protocolo.CodigoNaoEncontrado {
		t.Fatalf("esperava chave expirada, veio %v", err)
	}
	if _, err := cli.Del(ctx, []byte("estoque"), 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Get(ctx, []byte("estoque"), 0); protocolo.CodigoDe(err) != protocolo.CodigoNaoEncontrado {
		t.Fatalf("esperava chave apagada, veio %v", err)
	}
}

func TestTokenObrigatorio(t *testing.T) {
	c := teste.NovoCluster(t, 1, "segredo")
	cli, err := cliente.Novo(cliente.Config{Nos: []string{c.Nos[0].Endereco()}, Token: "errado", TempoLimite: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Fechar()
	if err := cli.Conectar(context.Background()); protocolo.CodigoDe(err) != protocolo.CodigoNaoAutorizado {
		t.Fatalf("esperava não autorizado, veio %v", err)
	}
}

func TestReparoDeLeituraEDicas(t *testing.T) {
	c := teste.NovoCluster(t, 3, "")
	cli := c.Cliente(t)
	ctx := context.Background()
	alvo := c.Nos[1]
	cfg := c.Configs[1]
	alvo.Derrubar()
	for i := 0; i < 30; i++ {
		k := fmt.Sprintf("k%02d", i)
		if _, err := cli.Put(ctx, []byte(k), []byte("v"), cliente.OpcoesPut{Consistencia: protocolo.ConsistenciaUm}); err != nil {
			t.Fatal(err)
		}
	}
	novo, err := no.Novo(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := novo.Iniciar(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { novo.Parar(context.Background()) })
	prazo := time.Now().Add(5 * time.Second)
	for time.Now().Before(prazo) {
		faltam := 0
		for i := 0; i < 30; i++ {
			k := []byte(fmt.Sprintf("k%02d", i))
			if !possui(novo, k) {
				continue
			}
			if _, ok, _ := novo.Motor().LerBruto(k); !ok {
				faltam++
			}
		}
		if faltam == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("as dicas não chegaram ao nó que voltou")
}

func possui(n *no.No, k []byte) bool {
	for _, d := range n.Donos(k) {
		if d.ID == n.ID() {
			return true
		}
	}
	return false
}

func TestScanPulaLapides(t *testing.T) {
	c := teste.NovoCluster(t, 3, "")
	cli := c.Cliente(t)
	ctx := context.Background()
	for i := 0; i < 300; i++ {
		k := []byte(fmt.Sprintf("a:%04d", i))
		cli.Put(ctx, k, []byte("x"), cliente.OpcoesPut{})
		cli.Del(ctx, k, 0, 0)
	}
	for i := 0; i < 5; i++ {
		cli.Put(ctx, []byte(fmt.Sprintf("b:%d", i)), []byte("y"), cliente.OpcoesPut{})
	}
	itens, mais, err := cli.Scan(ctx, cliente.OpcoesScan{Limite: 3})
	if err != nil || len(itens) != 3 || !mais || string(itens[0].Chave) != "b:0" {
		t.Fatalf("scan sobre lápides: %d %v %v", len(itens), mais, err)
	}
	itens, mais, err = cli.Scan(ctx, cliente.OpcoesScan{Limite: 3, Depois: "b:2"})
	if err != nil || len(itens) != 2 || mais {
		t.Fatalf("segunda página: %d %v %v", len(itens), mais, err)
	}
}

func TestSaidaDeNoTransfereChaves(t *testing.T) {
	c := teste.NovoCluster(t, 4, "")
	cli := c.Cliente(t)
	ctx := context.Background()
	for i := 0; i < 200; i++ {
		cli.Put(ctx, []byte(fmt.Sprintf("k%03d", i)), []byte("v"), cliente.OpcoesPut{Consistencia: protocolo.ConsistenciaTodos})
	}
	if err := cli.Sair(ctx, c.Nos[3].Endereco()); err != nil {
		t.Fatal(err)
	}
	prazo := time.Now().Add(5 * time.Second)
	for time.Now().Before(prazo) {
		if len(c.Nos[0].Topologia().Membros) == 4 && c.Nos[0].Donos([]byte("k000"))[0].ID != "" && semNo(c.Nos[0], "no-4") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	c.Nos[3].Derrubar()
	cli.Atualizar(ctx)
	for i := 0; i < 200; i++ {
		if _, err := cli.Get(ctx, []byte(fmt.Sprintf("k%03d", i)), protocolo.ConsistenciaTodos); err != nil {
			t.Fatalf("k%03d depois da saída: %v", i, err)
		}
	}
}

func semNo(n *no.No, id string) bool {
	for _, m := range n.Topologia().Membros {
		if m.ID == id {
			return m.Estado == "saiu"
		}
	}
	return true
}
