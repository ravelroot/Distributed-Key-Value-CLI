package teste

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/cliente"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/no"
)

type Cluster struct {
	Nos     []*no.No
	Configs []no.Config
	Token   string
}

func portaLivre(t testing.TB) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func NovoCluster(t testing.TB, n int, token string) *Cluster {
	t.Helper()
	dir := t.TempDir()
	c := &Cluster{Token: token}
	var sementes []string
	for i := 0; i < n; i++ {
		end := portaLivre(t)
		cfg := no.Config{ID: fmt.Sprintf("no-%d", i+1), Cluster: "teste", Endereco: end, Dados: dir, Sementes: append([]string{}, sementes...), Replicas: 3, VNodes: 64, Token: token, IntervaloGossip: 100 * time.Millisecond, TempoOperacao: 2 * time.Second}
		nd, err := no.Novo(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := nd.Iniciar(context.Background()); err != nil {
			t.Fatal(err)
		}
		cfg.Endereco = nd.Endereco()
		c.Nos = append(c.Nos, nd)
		c.Configs = append(c.Configs, cfg)
		sementes = append(sementes, nd.Endereco())
		t.Cleanup(func() {
			ctx, cancelar := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancelar()
			nd.Parar(ctx)
		})
	}
	prazo := time.Now().Add(5 * time.Second)
	for time.Now().Before(prazo) {
		ok := true
		for _, nd := range c.Nos {
			if len(nd.Topologia().Membros) != n {
				ok = false
			}
		}
		if ok {
			return c
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("o cluster de teste não convergiu")
	return nil
}

func (c *Cluster) Enderecos() []string {
	var out []string
	for _, n := range c.Nos {
		out = append(out, n.Endereco())
	}
	return out
}

func (c *Cluster) Cliente(t testing.TB) *cliente.Cliente {
	cli, err := cliente.Novo(cliente.Config{Nos: c.Enderecos(), Token: c.Token, TempoLimite: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cli.Fechar)
	if err := cli.Conectar(context.Background()); err != nil {
		t.Fatal(err)
	}
	return cli
}
