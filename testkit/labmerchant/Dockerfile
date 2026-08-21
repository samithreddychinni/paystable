FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o labmerchant ./testkit/labmerchant

FROM alpine:3.20
RUN adduser -D -H -s /sbin/nologin merchant
COPY --from=builder /app/labmerchant /labmerchant
USER merchant
ENTRYPOINT ["/labmerchant"]
