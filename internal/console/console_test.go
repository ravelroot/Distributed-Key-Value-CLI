package console_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/console"
	"github.com/ravelroot/Distributed-Key-Value-CLI/internal/teste"
)

func TestConsole(t *testing.T) {
	c := teste.NovoCluster(t, 3, "")
	cli := c.Cliente(t)
	srv, err := console.Novo(console.Config{Cliente: cli})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	req := func(metodo, url, corpo string, xleme bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(metodo, url, strings.NewReader(corpo))
		if xleme {
			r.Header.Set("X-Leme", "1")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := req("GET", "/", "", false); w.Code != 200 || !strings.Contains(w.Body.String(), "Leme") || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("página: %d", w.Code)
	}
	if w := req("PUT", "/api/v1/chave", `{"chave":"a","valor":"1"}`, false); w.Code != http.StatusForbidden {
		t.Fatalf("sem X-Leme deveria ser 403, veio %d", w.Code)
	}
	if w := req("PUT", "/api/v1/chave", `{"chave":"a","valor":"1","ttl":"1h"}`, true); w.Code != 200 {
		t.Fatalf("gravar: %d %s", w.Code, w.Body.String())
	}
	w := req("GET", "/api/v1/chave?k=a", "", false)
	var v struct {
		Item struct {
			Valor  string `json:"valor"`
			Expira int64  `json:"expira"`
		} `json:"item"`
		Replicas []any `json:"replicas"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Item.Valor != "1" || v.Item.Expira == 0 || len(v.Replicas) != 3 {
		t.Fatalf("ler: %d %s", w.Code, w.Body.String())
	}
	if w := req("GET", "/api/v1/chave?k=nada", "", false); w.Code != 404 {
		t.Fatalf("chave ausente deveria ser 404, veio %d", w.Code)
	}
	w = req("POST", "/api/v1/executar", `{"linha":"scan --valores"}`, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "1 chaves") {
		t.Fatalf("executar: %s", w.Body.String())
	}
	w = req("POST", "/api/v1/executar", `{"linha":"node leave no-1"}`, true)
	if !strings.Contains(w.Body.String(), "--confirmar") {
		t.Fatalf("comando perigoso deveria pedir confirmação: %s", w.Body.String())
	}
	if w := req("GET", "/api/v1/resumo", "", false); w.Code != 200 || !strings.Contains(w.Body.String(), `"vivos":3`) {
		t.Fatalf("resumo: %s", w.Body.String())
	}
	if w := req("DELETE", "/api/v1/chave?k=a", "", true); w.Code != 200 {
		t.Fatalf("apagar: %d", w.Code)
	}
}

func TestConsoleComToken(t *testing.T) {
	c := teste.NovoCluster(t, 1, "")
	srv, err := console.Novo(console.Config{Cliente: c.Cliente(t), Endereco: "0.0.0.0:0", Token: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/resumo", nil))
	if w.Code != 401 {
		t.Fatalf("sem token deveria ser 401, veio %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/?token=abc", nil))
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Set-Cookie"), "HttpOnly") {
		t.Fatalf("login por token: %d %v", w.Code, w.Header())
	}
	r := httptest.NewRequest("GET", "/api/v1/resumo", nil)
	r.Header.Set("Authorization", "Bearer abc")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("com token deveria ser 200, veio %d", w.Code)
	}
	if _, err := console.Novo(console.Config{Cliente: nil, Endereco: "0.0.0.0:8080"}); err == nil {
		t.Fatal("expor o console sem token deveria falhar")
	}
}

func TestConsoleTransferenciaEDiagnostico(t *testing.T) {
	c := teste.NovoCluster(t, 3, "")
	srv, err := console.Novo(console.Config{Cliente: c.Cliente(t)})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	req := func(metodo, url, corpo string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(metodo, url, strings.NewReader(corpo))
		r.Header.Set("X-Leme", "1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, k := range []string{"usuario:1", "usuario:2", "pedido:1"} {
		if w := req("PUT", "/api/v1/chave", `{"chave":"`+k+`","valor":"v-`+k+`","ttl":"1h"}`); w.Code != 200 {
			t.Fatalf("gravar %s: %d %s", k, w.Code, w.Body.String())
		}
	}
	w := req("GET", "/api/v1/exportar?prefixo=usuario:&formato=ndjson", "")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Disposition"), "leme-usuario-") || !strings.HasSuffix(strings.Split(w.Header().Get("Content-Disposition"), `"`)[1], ".ndjson") {
		t.Fatalf("exportar: %d %v", w.Code, w.Header())
	}
	linhas := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if len(linhas) != 2 || !strings.Contains(w.Body.String(), `"usuario:1"`) || strings.Contains(w.Body.String(), "pedido:1") {
		t.Fatalf("exportação deveria ter só as 2 chaves do prefixo: %q", w.Body.String())
	}
	if w := req("GET", "/api/v1/exportar?formato=xml", ""); w.Code != 400 {
		t.Fatalf("formato inválido deveria ser 400, veio %d", w.Code)
	}
	exportado := w.Body.String()
	w = req("POST", "/api/v1/importar?prefixo=copia:&nome=backup.ndjson&simular=1", exportado)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"lidos":2`) {
		t.Fatalf("simular importação: %d %s", w.Code, w.Body.String())
	}
	if w := req("GET", "/api/v1/chave?k=copia:usuario:1", ""); w.Code != 404 {
		t.Fatalf("simulação não deveria gravar, veio %d", w.Code)
	}
	w = req("POST", "/api/v1/importar?prefixo=copia:&nome=backup.ndjson", exportado)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"gravados":2`) {
		t.Fatalf("importar: %d %s", w.Code, w.Body.String())
	}
	var lido struct {
		Item struct {
			Valor  string `json:"valor"`
			Expira int64  `json:"expira"`
		} `json:"item"`
	}
	w = req("GET", "/api/v1/chave?k=copia:usuario:2", "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &lido) != nil || lido.Item.Valor != "v-usuario:2" || lido.Item.Expira == 0 {
		t.Fatalf("chave importada deveria manter valor e TTL: %d %s", w.Code, w.Body.String())
	}
	w = req("GET", "/api/v1/diagnostico", "")
	var d struct {
		Verificacoes []struct {
			Nome   string `json:"nome"`
			Estado string `json:"estado"`
		} `json:"verificacoes"`
		Contagem map[string]int `json:"contagem"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &d) != nil || len(d.Verificacoes) == 0 || d.Contagem["falha"] != 0 {
		t.Fatalf("diagnóstico: %d %s", w.Code, w.Body.String())
	}
	w = req("GET", "/api/v1/openapi.json", "")
	var spec struct {
		OpenAPI string                    `json:"openapi"`
		Paths   map[string]map[string]any `json:"paths"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &spec) != nil || !strings.HasPrefix(spec.OpenAPI, "3.1") {
		t.Fatalf("openapi: %d", w.Code)
	}
	for _, rota := range []string{"/api/v1/chave", "/api/v1/exportar", "/api/v1/importar", "/api/v1/diagnostico", "/api/v1/resumo"} {
		if spec.Paths[rota] == nil {
			t.Fatalf("openapi sem a rota %s", rota)
		}
	}
	if w := req("GET", "/api/v1/integracao", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"protegido":false`) {
		t.Fatalf("integração: %s", w.Body.String())
	}
}

func TestConsoleBearerDispensaXLeme(t *testing.T) {
	c := teste.NovoCluster(t, 1, "")
	srv, err := console.Novo(console.Config{Cliente: c.Cliente(t), Endereco: "0.0.0.0:0", Token: "segredo"})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	enviar := func(auth string, xleme bool) int {
		r := httptest.NewRequest("PUT", "/api/v1/chave", strings.NewReader(`{"chave":"b","valor":"1"}`))
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		if xleme {
			r.Header.Set("X-Leme", "1")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if code := enviar("Bearer segredo", false); code != 200 {
		t.Fatalf("Bearer válido deveria dispensar X-Leme, veio %d", code)
	}
	if code := enviar("Bearer errado", false); code != http.StatusForbidden {
		t.Fatalf("Bearer inválido sem X-Leme deveria ser 403, veio %d", code)
	}
	if code := enviar("Bearer errado", true); code != http.StatusUnauthorized {
		t.Fatalf("Bearer inválido com X-Leme deveria ser 401, veio %d", code)
	}
	r := httptest.NewRequest("PUT", "/api/v1/chave", strings.NewReader(`{"chave":"b","valor":"1"}`))
	r.AddCookie(&http.Cookie{Name: "leme_console", Value: "segredo"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cookie sem X-Leme deveria continuar bloqueado (CSRF), veio %d", w.Code)
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/api/v1/integracao", nil)
	r.Header.Set("Authorization", "Bearer segredo")
	h.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), `"protegido":true`) {
		t.Fatalf("integração protegida: %s", w.Body.String())
	}
}
