# DO NOT USE IN PRODUCTION — local dev only.
FROM golang:1.26-alpine AS builder
WORKDIR /app

COPY cmd/auth-dev/ ./cmd/auth-dev/
ENV GOWORK=off
WORKDIR /app/cmd/auth-dev

RUN go build -o /auth-dev ./...

FROM alpine:3.21
RUN apk --no-cache add ca-certificates
COPY --from=builder /auth-dev /auth-dev
EXPOSE 3001
ENTRYPOINT ["/auth-dev"]
