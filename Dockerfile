FROM golang:1.27-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/multiverse ./cmd/server

FROM scratch
COPY --from=builder /out/multiverse /multiverse
EXPOSE 8080
ENTRYPOINT ["/multiverse"]
