# Build from examples/shop: docker build -f services/payments/Dockerfile -t shop-payments .
FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY internal/ internal/
COPY services/payments/ services/payments/
COPY cmd/payments/ cmd/payments/
RUN CGO_ENABLED=0 go build -trimpath -o /payments ./cmd/payments

# alpine rather than scratch, for wget in the compose healthcheck.
FROM alpine:3.20
COPY --from=build /payments /payments
EXPOSE 8081
ENTRYPOINT ["/payments"]
