#!/usr/bin/env bash
set -euo pipefail
BIN="${1:-./bin}"
DADOS="$(mktemp -d)"
export LEME_TOKEN="${LEME_TOKEN:-token-da-integracao-continua}"
export LEME_NOS="127.0.0.1:7501,127.0.0.1:7502,127.0.0.1:7503"
PIDS=()
encerrar() { kill "${PIDS[@]}" 2>/dev/null || true; rm -rf "$DADOS"; }
trap encerrar EXIT

"$BIN/lemed" --id no-1 --endereco 127.0.0.1:7501 --dados "$DADOS" --metricas 127.0.0.1:9501 --log warn & PIDS+=($!)
sleep 1
"$BIN/lemed" --id no-2 --endereco 127.0.0.1:7502 --dados "$DADOS" --entrar 127.0.0.1:7501 --log warn & PIDS+=($!)
"$BIN/lemed" --id no-3 --endereco 127.0.0.1:7503 --dados "$DADOS" --entrar 127.0.0.1:7501 --log warn & PIDS+=($!)

for _ in $(seq 1 30); do
  if [ "$("$BIN/leme" cluster nodes -o json 2>/dev/null | grep -c '"estado": "vivo"')" = "3" ]; then break; fi
  sleep 1
done
"$BIN/leme" cluster nodes

"$BIN/leme" put usuario:1 '{"nome":"Ana"}' --consistencia todos
test "$("$BIN/leme" get usuario:1)" = '{"nome":"Ana"}'
printf 'put pedido:1 um\nput pedido:2 dois\ncount pedido:\n' | "$BIN/leme" shell | grep -qx 2
"$BIN/leme" bench --operacoes 2000 --concorrencia 16

# 1.1.0: contadores, cópia, várias chaves, backup lógico e diagnóstico
test "$("$BIN/leme" incr visitas 5)" = 5
test "$("$BIN/leme" decr visitas)" = 4
"$BIN/leme" mset config:tema=escuro config:idioma=pt-BR
"$BIN/leme" rename config:tema config:tema-antigo
"$BIN/leme" export config: --arquivo "$DADOS/config.ndjson"
"$BIN/leme" import "$DADOS/config.ndjson" --prefixo restaurado:
test "$("$BIN/leme" get restaurado:config:tema-antigo)" = escuro
"$BIN/leme" doctor
"$BIN/leme" top --vezes 1

kill "${PIDS[2]}"
sleep 4
"$BIN/leme" get usuario:1 --consistencia quorum

set +e
"$BIN/leme" get nao-existe; test $? -eq 3 || exit 1
"$BIN/leme" --token errado get usuario:1; test $? -eq 7 || exit 1
"$BIN/leme" doctor; test $? -eq 8 || exit 1
set -e

curl -fsS http://127.0.0.1:9501/saude | grep -q '"status":"ok"'
curl -fsS http://127.0.0.1:9501/metricas | grep -q leme_operacoes_total
echo "ponta a ponta: tudo certo"
