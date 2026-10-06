FROM golang:1.22-alpine AS build
WORKDIR /src

COPY server/go.mod server/go.sum ./server/
WORKDIR /src/server
RUN go mod download

COPY server ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/syncforge ./cmd/syncforge

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/syncforge /app/syncforge
RUN mkdir -p /data && chmod 700 /data
EXPOSE 8080
ENTRYPOINT ["/app/syncforge"]
