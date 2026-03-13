FROM golang:1.26-alpine AS builder
WORKDIR /app

COPY go.work go.work.sum* ./
COPY shared/ ./shared/
COPY services/search/ ./services/search/

RUN go build -o /search ./services/search/...

FROM alpine:3.21
RUN apk --no-cache add ca-certificates
COPY --from=builder /search /search
EXPOSE 8082
ENTRYPOINT ["/search"]
