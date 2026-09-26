FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/mihomo-observer ./cmd/mihomo-observer

FROM alpine:3.22
RUN addgroup -S observer && adduser -S -G observer observer && mkdir /data && chown observer:observer /data
COPY --from=build /out/mihomo-observer /usr/local/bin/mihomo-observer
USER observer
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/mihomo-observer"]
CMD ["-config", "/etc/observer/config.yaml"]
