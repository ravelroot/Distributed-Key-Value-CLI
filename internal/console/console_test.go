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
