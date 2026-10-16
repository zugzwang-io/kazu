# One image with both binaries, as Kazu's single image would carry them.
# Build from spikes/network: docker build -t kazu-spike .
FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/ ./proxy ./helper

FROM alpine:3.20
COPY --from=build /out/proxy /out/helper /usr/local/bin/
