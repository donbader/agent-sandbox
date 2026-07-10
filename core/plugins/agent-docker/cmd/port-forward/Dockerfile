FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod .
COPY *.go .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /port-forward .

FROM scratch
COPY --from=builder /port-forward /port-forward
ENTRYPOINT ["/port-forward"]
