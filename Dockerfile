FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -o /rds-test .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates

COPY --from=builder /rds-test /rds-test


CMD ["/rds-test"]