FROM golang:1.27.0-alpine3.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /app/server ./cmd/server

FROM alpine:3.23.0
RUN apk add --no-cache ca-certificates && addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /app/server /app/server
USER app
EXPOSE 8080
ENTRYPOINT ["/app/server"]
