FROM golang:1.26-alpine AS builder
WORKDIR /app

COPY models/ ./models/
COPY shared/ ./shared/
COPY repository/ ./repository/
COPY activities/ ./activities/
COPY workflows/ ./workflows/
COPY cmd/worker/ ./cmd/worker/

ENV GOWORK=off
WORKDIR /app/cmd/worker

RUN go build -o /worker .

FROM alpine:3.21
RUN apk --no-cache add ca-certificates
COPY --from=builder /worker /worker
ENTRYPOINT ["/worker"]
