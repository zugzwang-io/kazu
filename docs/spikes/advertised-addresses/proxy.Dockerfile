FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /kazu ./cmd/kazu

FROM alpine:3.20
COPY --from=build /kazu /kazu
ENTRYPOINT ["/kazu"]
