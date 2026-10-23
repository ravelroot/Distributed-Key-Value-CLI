#!/usr/bin/env sh
set -e
cd "$(dirname "$0")"

abrir() {
  if command -v xdg-open >/dev/null 2>&1; then xdg-open "$1" >/dev/null 2>&1 || true
  elif command -v open >/dev/null 2>&1; then open "$1" || true
  fi
}

aleatorio() {
  if command -v openssl >/dev/null 2>&1; then openssl rand -hex 24
  else head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  if [ ! -f .env ]; then
    cp .env.example .env
    sed -i.bak "s/^LEME_TOKEN=.*/LEME_TOKEN=$(aleatorio)/; s/^LEME_TOKEN_CONSOLE=.*/LEME_TOKEN_CONSOLE=$(aleatorio)/" .env && rm -f .env.bak
    echo "Arquivo .env criado com tokens aleatórios."
  fi
  docker compose up -d --build
  . ./.env
  URL="http://127.0.0.1:${LEME_PORTA_CONSOLE:-8080}/?token=${LEME_TOKEN_CONSOLE}"
  echo ""
  echo "Cluster com 3 nós no ar."
  echo "Console:          $URL"
  echo "Modo interativo:  docker compose exec console leme --nos no1:7400"
  abrir "$URL"
  exit 0
fi

if command -v go >/dev/null 2>&1; then
  mkdir -p bin
  go build -o bin/leme ./cmd/leme
  go build -o bin/lemed ./cmd/lemed
  (sleep 2 && abrir "http://127.0.0.1:8080/") &
  exec ./bin/leme dev --console 127.0.0.1:8080
fi

echo "Instale o Docker (com Docker Compose) ou o Go 1.24+ para rodar o Leme."
exit 1
