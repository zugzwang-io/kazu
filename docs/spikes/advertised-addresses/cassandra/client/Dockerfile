FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go .
RUN CGO_ENABLED=0 go build -o /cqlclient .

FROM alpine:3.20
COPY --from=build /cqlclient /cqlclient
ENTRYPOINT ["sleep", "infinity"]
