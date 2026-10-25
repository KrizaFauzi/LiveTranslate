FROM golang:1.27.0-alpine3.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /app/server ./cmd/server \
    && CGO_ENABLED=0 go build -trimpath -o /app/storage-cleanup ./cmd/storage-cleanup

FROM alpine:3.23.0
RUN apk add --no-cache ca-certificates poppler-utils tesseract-ocr tesseract-ocr-data-eng tesseract-ocr-data-ind && addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /app/server /app/server
COPY --from=build /app/storage-cleanup /app/storage-cleanup
USER app
EXPOSE 8080
ENTRYPOINT ["/app/server"]
