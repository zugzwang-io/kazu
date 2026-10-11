# One image for every shop service; compose picks the service with `command`.
FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /shop ./cmd/shop

FROM scratch
COPY --from=build /shop /shop
COPY certs/ca.pem certs/fakestripe.pem certs/fakestripe-key.pem /certs/
ENTRYPOINT ["/shop"]
