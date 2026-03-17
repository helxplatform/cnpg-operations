FROM golang:1.21-alpine AS builder
WORKDIR /app

# Cache dependencies separately from source
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o cnpg-rest-server \
    ./cmd/server

# Minimal runtime image
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /app/cnpg-rest-server /cnpg-rest-server

EXPOSE 8080
ENTRYPOINT ["/cnpg-rest-server"]
