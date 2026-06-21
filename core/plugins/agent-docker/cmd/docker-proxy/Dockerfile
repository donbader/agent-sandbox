FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY *.go ./
RUN go mod init docker-proxy && CGO_ENABLED=0 go build -o /docker-proxy .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates iptables curl
COPY --from=builder /docker-proxy /usr/local/bin/docker-proxy
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
