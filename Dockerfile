FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go/go.mod ./
RUN go mod download
COPY go/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /canicule .

FROM alpine:3.21
RUN addgroup -S canicule && adduser -S -G canicule canicule
WORKDIR /data
COPY --from=build /canicule /usr/local/bin/canicule
COPY config.json /defaults/config.json
COPY go/templates/ /defaults/templates/
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint
RUN chmod +x /usr/local/bin/docker-entrypoint && chown -R canicule:canicule /data /defaults
USER canicule
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["docker-entrypoint"]
CMD ["--web", "--web-address", ":8080", "-c", "/data/config.json"]
