FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o razorpaymerchant ./testkit/razorpaymerchant

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -H -s /sbin/nologin merchant
COPY --from=builder /app/razorpaymerchant /razorpaymerchant
USER merchant
ENTRYPOINT ["/razorpaymerchant"]
