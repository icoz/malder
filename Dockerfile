FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o malder-server ./cmd/server

FROM alpine:latest

RUN apk --no-cache add ca-certificates pandoc poppler-utils

WORKDIR /root/

COPY --from=builder /app/malder-server .

RUN mkdir -p /data

EXPOSE 8080

CMD ["./malder-server"]
