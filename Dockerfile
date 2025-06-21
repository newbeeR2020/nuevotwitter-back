# ---- 1) ビルド段階 ----
FROM golang:1.24 AS builder
WORKDIR /workspace
COPY go.mod ./
COPY go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -o app .

# ---- 2) 実行段階 ----
FROM gcr.io/distroless/static-debian12
WORKDIR /
COPY --from=builder /workspace/app /app
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app"]