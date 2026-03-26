FROM golang:1.24-bookworm AS builder
RUN apt-get update && apt-get install -y gcc libc6-dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -tags fts5 -o /virgil ./cmd/virgil

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates wget && \
    wget -qO- https://github.com/benbjohnson/litestream/releases/download/v0.3.13/litestream-v0.3.13-linux-amd64.tar.gz | \
    tar -xz -C /usr/local/bin litestream && \
    apt-get remove -y wget && apt-get autoremove -y && rm -rf /var/lib/apt/lists/*
COPY --from=builder /virgil /usr/local/bin/virgil
COPY deploy/litestream.yml /etc/litestream.yml
COPY deploy/entrypoint.sh /usr/local/bin/entrypoint.sh
COPY config/virgil.yaml /etc/virgil/default.yaml
COPY skills/ /etc/virgil/skills/
RUN chmod +x /usr/local/bin/entrypoint.sh

EXPOSE 8080
CMD ["/usr/local/bin/entrypoint.sh"]
