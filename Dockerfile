FROM golang:1.24-alpine AS construcao
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSAO=1.0.0
ARG COMMIT=docker
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/ravelroot/Distributed-Key-Value-CLI/internal/versao.Versao=${VERSAO} -X github.com/ravelroot/Distributed-Key-Value-CLI/internal/versao.Commit=${COMMIT}" -o /saida/leme ./cmd/leme \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/ravelroot/Distributed-Key-Value-CLI/internal/versao.Versao=${VERSAO} -X github.com/ravelroot/Distributed-Key-Value-CLI/internal/versao.Commit=${COMMIT}" -o /saida/lemed ./cmd/lemed

FROM alpine:3.20
RUN addgroup -S leme && adduser -S -G leme -h /var/lib/leme leme && apk add --no-cache ca-certificates wget
COPY --from=construcao /saida/leme /saida/lemed /usr/local/bin/
USER leme
WORKDIR /var/lib/leme
ENV LEME_ENDERECO=0.0.0.0:7400 LEME_DADOS=/var/lib/leme LEME_METRICAS=0.0.0.0:9400
EXPOSE 7400 9400
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 CMD wget -qO- http://127.0.0.1:9400/saude || exit 1
ENTRYPOINT ["lemed"]
