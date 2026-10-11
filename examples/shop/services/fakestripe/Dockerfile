# Build from examples/shop: docker build -f services/fakestripe/Dockerfile -t shop-fakestripe .
FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY internal/ internal/
COPY services/fakestripe/ services/fakestripe/
COPY cmd/fakestripe/ cmd/fakestripe/
RUN CGO_ENABLED=0 go build -trimpath -o /fakestripe ./cmd/fakestripe

FROM scratch
COPY --from=build /fakestripe /fakestripe
EXPOSE 12111
ENTRYPOINT ["/fakestripe"]
