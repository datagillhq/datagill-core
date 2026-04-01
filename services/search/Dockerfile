FROM golang:1.26-alpine AS builder
WORKDIR /app

COPY models/ ./models/
COPY repository/ ./repository/
COPY services/search/ ./services/search/
ENV GOWORK=off
WORKDIR /app/services/search

RUN go build -o /search ./...

FROM alpine:3.21
RUN apk --no-cache add ca-certificates
COPY --from=builder /search /search
EXPOSE 8082
ENTRYPOINT ["/search"]
