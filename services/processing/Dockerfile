FROM golang:1.26-alpine AS builder
WORKDIR /app

COPY go.work go.work.sum* ./
COPY shared/ ./shared/
COPY services/processing/ ./services/processing/

RUN go build -o /processing ./services/processing/...

FROM alpine:3.21
RUN apk --no-cache add ca-certificates
COPY --from=builder /processing /processing
EXPOSE 8081
ENTRYPOINT ["/processing"]
