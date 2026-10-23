package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/armazenamento"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/no"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/versao"
)

func env(nome, padrao string) string {
	if v := os.Getenv(nome); v != "" {
		return v
	}
	return padrao
}

func envInt(nome string, padrao int) int {
	if v, err := strconv.Atoi(os.Getenv(nome)); err == nil {
		return v
	}
	return padrao
}

func main() {
	var cfg no.Config
	var sementes, sinc, nivel, formato, tokenArquivo string
	var segmentoMB int
	var graca time.Duration
	cmd := &cobra.Command{
		Use:           "lemed",
		Short:         "Nó do cluster chave-valor do Leme",
		Long:          "lemed é o processo de cada nó do cluster: guarda as chaves em um log com índice em memória, replica as escritas para as réplicas do anel e atende a CLI pelo protocolo binário.",
		Example:       "  lemed --id no-1 --endereco 0.0.0.0:7400 --anunciar 10.0.0.11:7400 --dados /var/lib/leme\n  lemed --id no-2 --endereco 0.0.0.0:7400 --anunciar 10.0.0.12:7400 --entrar 10.0.0.11:7400",
		Version:       versao.Versao,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var lv slog.Level
			if err := lv.UnmarshalText([]byte(nivel)); err != nil {
				return fmt.Errorf("nível de log inválido: %s", nivel)
			}
			var h slog.Handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lv})
			if formato == "json" {
				h = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lv})
			}
			cfg.Log = slog.New(h)
			s, err := armazenamento.ParseSincronizacao(sinc)
			if err != nil {
				return err
			}
			cfg.Armazenamento = armazenamento.Opcoes{Sincronizar: s, SegmentoMax: int64(segmentoMB) << 20, GracaLapide: graca}
			for _, x := range strings.Split(sementes, ",") {
				if x = strings.TrimSpace(x); x != "" {
					cfg.Sementes = append(cfg.Sementes, x)
				}
			}
			if cfg.Token == "" {
				if arq := tokenArquivo; arq != "" {
					b, err := os.ReadFile(arq)
					if err != nil {
						return err
					}
					cfg.Token = strings.TrimSpace(string(b))
				}
			}
			n, err := no.Novo(cfg)
			if err != nil {
				return err
			}
			ctx, cancelar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancelar()
			if err := n.Iniciar(ctx); err != nil {
				return err
			}
			<-ctx.Done()
			cfg.Log.Info("encerrando o nó")
			c, fim := context.WithTimeout(context.Background(), 15*time.Second)
			defer fim()
			return n.Parar(c)
		},
	}
	f := cmd.Flags()
	f.StringVar(&cfg.ID, "id", env("LEME_ID", ""), "identificador único do nó (LEME_ID)")
	f.StringVar(&cfg.Cluster, "cluster", env("LEME_CLUSTER", "leme"), "nome do cluster; nós de clusters diferentes se recusam (LEME_CLUSTER)")
	f.StringVar(&cfg.Endereco, "endereco", env("LEME_ENDERECO", "127.0.0.1:7400"), "endereço TCP onde o nó escuta (LEME_ENDERECO)")
	f.StringVar(&cfg.Anunciar, "anunciar", env("LEME_ANUNCIAR", ""), "endereço que os outros nós e a CLI usam para chegar aqui (LEME_ANUNCIAR)")
	f.StringVar(&cfg.Dados, "dados", env("LEME_DADOS", "dados"), "pasta dos segmentos de log (LEME_DADOS)")
	f.StringVar(&sementes, "entrar", env("LEME_ENTRAR", ""), "nós já existentes para entrar no cluster, separados por vírgula (LEME_ENTRAR)")
	f.IntVar(&cfg.Replicas, "replicas", envInt("LEME_REPLICAS", 3), "cópias de cada chave (LEME_REPLICAS)")
	f.IntVar(&cfg.VNodes, "vnodes", envInt("LEME_VNODES", 128), "nós virtuais por nó no anel (LEME_VNODES)")
	f.StringVar(&cfg.Token, "token", env("LEME_TOKEN", ""), "token exigido de clientes e pares (LEME_TOKEN ou LEME_TOKEN_ARQUIVO)")
	f.StringVar(&tokenArquivo, "token-arquivo", env("LEME_TOKEN_ARQUIVO", ""), "lê o token de um arquivo (LEME_TOKEN_ARQUIVO)")
	f.StringVar(&cfg.TLS.Certificado, "tls-cert", env("LEME_TLS_CERT", ""), "certificado PEM do nó (ativa TLS)")
	f.StringVar(&cfg.TLS.Chave, "tls-chave", env("LEME_TLS_CHAVE", ""), "chave privada PEM do nó")
	f.StringVar(&cfg.TLS.CA, "tls-ca", env("LEME_TLS_CA", ""), "CA dos clientes e pares (ativa mTLS)")
	f.StringVar(&cfg.Metricas, "metricas", env("LEME_METRICAS", ""), "endereço HTTP de /saude e /metricas, ex.: 0.0.0.0:9400 (LEME_METRICAS)")
	f.DurationVar(&cfg.IntervaloGossip, "gossip", time.Second, "intervalo entre rodadas de gossip")
	f.DurationVar(&cfg.TempoOperacao, "tempo-operacao", 3*time.Second, "tempo máximo de uma operação entre nós")
	f.IntVar(&cfg.MaxConexoes, "max-conexoes", envInt("LEME_MAX_CONEXOES", 4096), "conexões simultâneas aceitas")
	f.StringVar(&sinc, "sincronizar", env("LEME_SINCRONIZAR", "intervalo"), "fsync: sempre, intervalo (1s) ou nunca (LEME_SINCRONIZAR)")
	f.IntVar(&segmentoMB, "segmento-mb", envInt("LEME_SEGMENTO_MB", 64), "tamanho máximo de cada segmento de log, em MiB")
	f.DurationVar(&graca, "graca-lapide", 24*time.Hour, "tempo que uma lápide fica no disco antes da compactação removê-la")
	f.StringVar(&nivel, "log", env("LEME_LOG", "info"), "nível de log: debug, info, warn ou error (LEME_LOG)")
	f.StringVar(&formato, "log-formato", env("LEME_LOG_FORMATO", "texto"), "formato do log: texto ou json")
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}
