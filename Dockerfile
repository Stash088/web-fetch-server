FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/web-fetch-server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/web-fetch-server /usr/local/bin/web-fetch-server
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/web-fetch-server"]
