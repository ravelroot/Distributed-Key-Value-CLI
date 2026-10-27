# Histórico de versões

## 1.1.0 · 27 de outubro de 2026

### CLI
- `leme export [prefixo]` e `leme import <arquivo | ->`: backup lógico em NDJSON, JSON ou CSV, com TTL restante, versão e valores binários em base64. A importação grava com até 16 escritas em paralelo e aceita `--se-ausente`, `--prefixo` e `--simular`.
- `leme incr` e `leme decr`: contadores atômicos por escrita condicional, com novas tentativas em conflito.
- `leme copy` e `leme rename`: copiam ou movem uma chave com valor e TTL; sem `--substituir` não sobrescrevem o destino.
- `leme mget` e `leme mset`: várias chaves em paralelo.
- `leme doctor`: diagnóstico do cluster (nós semente, token, membros, quórum, anel, dicas, fragmentação, latência, erros, versões e segurança) com sugestão para cada aviso; novo código de saída 8.
- `leme top`: painel ao vivo no terminal com operações por segundo, p99, disco, fragmentação e fatia do anel.
- `leme watch`: acompanha uma chave ou um prefixo e mostra cada mudança (ou emite eventos JSON).
- Números negativos agora funcionam como argumento no modo direto (`leme incr estoque:7 -3`, `leme put saldo -5`).

### Cluster
- Escritas condicionais (`--se-versao`, `--se-ausente`) são serializadas por chave no coordenador e gravadas com pelo menos quórum. Antes, duas escritas concorrentes com a mesma versão esperada podiam ser aceitas.

### Console web e API
- Layout novo, mantendo o logo e a paleta preto e branco: barra lateral por áreas, faixa de status, sparklines, anel em mostrador, feed de eventos, paleta de comandos (`Ctrl K`) e atalhos de teclado.
- Exportar e importar na página Chaves; páginas novas Diagnóstico e Integrações.
- Rotas `/api/v1/exportar`, `/api/v1/importar`, `/api/v1/diagnostico` e `/api/v1/openapi.json`; autenticação por `Authorization: Bearer`.

### Integrações
- `deploy/kubernetes/leme.yaml`, `deploy/systemd/`, `deploy/prometheus/` (coleta e alertas) e `deploy/grafana/` (painel e provisionamento).
- Perfil `observabilidade` no Docker Compose com Prometheus e Grafana.
- Alvos novos no Makefile: `ponta-a-ponta`, `docker`, `observabilidade` e `backup`.
- CI valida os manifestos de `deploy/` e exercita os comandos novos de ponta a ponta.

### Documentação
- Manual técnico e PDF do banco de dados refeitos com a nova diagramação e o conteúdo da 1.1.0; README atualizado.

## 1.0.0 · 23 de outubro de 2026

- Primeira versão: CLI interativa e modo direto, nós `lemed` com hash consistente, replicação com quórum, relógio híbrido, reparo de leitura, dicas, gossip, log com CRC32C e compactação, TLS, mTLS e token, console web, `leme dev` e `leme bench`.
