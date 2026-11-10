FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/orders ./cmd/orders

FROM alpine:3.20
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/orders /usr/local/bin/orders
EXPOSE 8082
ENTRYPOINT ["orders"]
