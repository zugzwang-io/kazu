# Build from examples/shop: docker build -f services/checkout/Dockerfile -t shop-checkout .
FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY internal/ internal/
COPY services/checkout/ services/checkout/
COPY cmd/checkout/ cmd/checkout/
RUN CGO_ENABLED=0 go build -trimpath -o /checkout ./cmd/checkout

# alpine rather than scratch, for wget in the compose healthcheck.
FROM alpine:3.20
COPY --from=build /checkout /checkout
EXPOSE 8080
ENTRYPOINT ["/checkout"]
