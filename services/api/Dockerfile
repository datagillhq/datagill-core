FROM golang:1.26-alpine AS builder
WORKDIR /app

COPY models/ ./models/
COPY repository/ ./repository/
COPY services/api/ ./services/api/
ENV GOWORK=off
WORKDIR /app/services/api

RUN go build -o /api .

FROM alpine:3.21
RUN apk --no-cache add ca-certificates
COPY --from=builder /api /api
EXPOSE 8082
ENTRYPOINT ["/api"]
