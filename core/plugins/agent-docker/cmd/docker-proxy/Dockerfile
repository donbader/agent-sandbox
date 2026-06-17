FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /docker-proxy ./core/plugins/agent-docker/cmd/docker-proxy/

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /docker-proxy /usr/local/bin/docker-proxy
ENTRYPOINT ["docker-proxy"]
