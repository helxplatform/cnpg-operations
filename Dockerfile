FROM golang:1.21-bookworm AS builder
WORKDIR /app

# Cache dependencies separately from source
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-w -s" \
    -o /bin/cnpg-rest-server \
    ./cmd/server

FROM debian:bookworm-slim
RUN useradd --no-create-home --uid 65532 --shell /bin/false nonroot
COPY --from=builder /bin/cnpg-rest-server /cnpg-rest-server

USER nonroot
EXPOSE 8080
ENTRYPOINT ["/cnpg-rest-server"]
