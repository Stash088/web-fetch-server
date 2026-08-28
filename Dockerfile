FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/web-fetch-server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
# Optional headless browser for JS rendering (web_fetch render=true / JS_RENDER).
# Enabled with: docker build --build-arg WITH_JS_RENDER=1
ARG WITH_JS_RENDER=0
RUN if [ "$WITH_JS_RENDER" = "1" ]; then \
        apk add --no-cache chromium font-noto-cjk fontconfig; \
    fi
COPY --from=build /out/web-fetch-server /usr/local/bin/web-fetch-server
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/web-fetch-server"]
