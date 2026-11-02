FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git ca-certificates tzdata
WORKDIR /src
COPY go.mod go.sum* ./
RUN if [ -f go.sum ]; then go mod download; fi
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gmessages-rag ./cmd/gmessages-rag

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata curl
WORKDIR /app
COPY --from=builder /out/gmessages-rag /usr/local/bin/gmessages-rag
RUN mkdir -p /data/auth /data/media /data/dossiers
EXPOSE 8090
ENTRYPOINT ["gmessages-rag"]
CMD ["serve"]
