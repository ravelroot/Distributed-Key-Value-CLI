package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type Perfil struct {
	Nos          []string `json:"nos"`
	Token        string   `json:"token,omitempty"`
	TokenArquivo string   `json:"token_arquivo,omitempty"`
	TLSCA        string   `json:"tls_ca,omitempty"`
	TLSCert      string   `json:"tls_cert,omitempty"`
	TLSChave     string   `json:"tls_chave,omitempty"`
	TLSInseguro  bool     `json:"tls_inseguro,omitempty"`
	Consistencia string   `json:"consistencia,omitempty"`
	TempoLimite  string   `json:"tempo_limite,omitempty"`
	Saida        string   `json:"saida,omitempty"`
}

type Arquivo struct {
	Atual   string            `json:"atual"`
	Perfis  map[string]Perfil `json:"perfis"`
	caminho string
}

func CaminhoPadrao() string {
	if p := os.Getenv("LEME_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "leme", "config.json")
}

func DiretorioDados() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "."
	}
	return filepath.Join(dir, "leme")
}

func Carregar(caminho string) (*Arquivo, error) {
	if caminho == "" {
		caminho = CaminhoPadrao()
	}
	a := &Arquivo{Perfis: map[string]Perfil{}, caminho: caminho}
	b, err := os.ReadFile(caminho)
	if errors.Is(err, os.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, a); err != nil {
		return nil, fmt.Errorf("arquivo de configuração inválido (%s): %w", caminho, err)
	}
	if a.Perfis == nil {
		a.Perfis = map[string]Perfil{}
	}
	return a, nil
}

func (a *Arquivo) Caminho() string { return a.caminho }

func (a *Arquivo) Salvar() error {
	if err := os.MkdirAll(filepath.Dir(a.caminho), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.caminho + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.caminho)
}

func (a *Arquivo) Nomes() []string {
	var out []string
	for n := range a.Perfis {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (a *Arquivo) Perfil(nome string) (Perfil, bool) {
	if nome == "" {
		nome = a.Atual
	}
	p, ok := a.Perfis[nome]
	return p, ok
}
