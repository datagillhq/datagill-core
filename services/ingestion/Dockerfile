FROM golang:1.26-alpine AS builder
WORKDIR /app

# Copy workspace and shared module first for layer caching
COPY go.work go.work.sum* ./
COPY shared/ ./shared/
COPY services/ingestion/ ./services/ingestion/

RUN go build -o /ingestion ./services/ingestion/...

FROM alpine:3.21
RUN apk --no-cache add ca-certificates
COPY --from=builder /ingestion /ingestion
EXPOSE 8080
ENTRYPOINT ["/ingestion"]
