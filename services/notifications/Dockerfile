FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/notifications ./cmd/notifications

FROM alpine:3.20
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/notifications /usr/local/bin/notifications
EXPOSE 8083
ENTRYPOINT ["notifications"]
