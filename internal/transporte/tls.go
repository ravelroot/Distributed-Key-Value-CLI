package transporte

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

type ArquivosTLS struct {
	CA           string
	Certificado  string
	Chave        string
	Inseguro     bool
	NomeServidor string
}

func (a ArquivosTLS) Ativo() bool {
	return a.CA != "" || a.Certificado != "" || a.Inseguro
}

func carregarCA(caminho string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(caminho)
	if err != nil {
		return nil, fmt.Errorf("ler CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("o arquivo de CA não contém certificados PEM válidos")
	}
	return pool, nil
}

func (a ArquivosTLS) Cliente() (*tls.Config, error) {
	if !a.Ativo() {
		return nil, nil
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: a.Inseguro, ServerName: a.NomeServidor}
	if a.CA != "" {
		pool, err := carregarCA(a.CA)
		if err != nil {
			return nil, err
		}
		cfg.RootCAs = pool
	}
	if a.Certificado != "" || a.Chave != "" {
		par, err := tls.LoadX509KeyPair(a.Certificado, a.Chave)
		if err != nil {
			return nil, fmt.Errorf("carregar certificado do cliente: %w", err)
		}
		cfg.Certificates = []tls.Certificate{par}
	}
	return cfg, nil
}

func (a ArquivosTLS) Servidor() (*tls.Config, error) {
	if a.Certificado == "" && a.Chave == "" {
		return nil, nil
	}
	par, err := tls.LoadX509KeyPair(a.Certificado, a.Chave)
	if err != nil {
		return nil, fmt.Errorf("carregar certificado do servidor: %w", err)
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{par}}
	if a.CA != "" {
		pool, err := carregarCA(a.CA)
		if err != nil {
			return nil, err
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
		cfg.RootCAs = pool
	}
	return cfg, nil
}
