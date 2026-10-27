VERSAO := $(shell cat VERSION)
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/ravelroot/Distributed-Key-Value-CLI/internal/versao.Versao=$(VERSAO) -X github.com/ravelroot/Distributed-Key-Value-CLI/internal/versao.Commit=$(COMMIT)

.PHONY: build testar verificar dev ponta-a-ponta docker observabilidade backup limpar

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/ ./cmd/...

testar:
	go test -race -count=1 ./...

verificar:
	gofmt -l . && go vet ./...

dev: build
	./bin/leme dev --console 127.0.0.1:8080

ponta-a-ponta: build
	bash scripts/ponta-a-ponta.sh bin

docker:
	docker build --build-arg VERSAO=$(VERSAO) --build-arg COMMIT=$(COMMIT) -t leme:$(VERSAO) .

observabilidade:
	docker compose --profile observabilidade up -d

backup: build
	./bin/leme export --arquivo backup-$$(date +%Y%m%d-%H%M%S).ndjson

limpar:
	rm -rf bin dist
