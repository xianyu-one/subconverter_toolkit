FROM golang:1.25-alpine AS build
WORKDIR /src
ARG GOPROXY=https://proxy.golang.org
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/geox-sync .

FROM alpine:3.22
RUN addgroup -S geox && adduser -S -G geox geox && mkdir /data && chown geox:geox /data
COPY --from=build /out/geox-sync /usr/local/bin/geox-sync
USER geox
VOLUME /data
ENV GEOX_SYNC_OUTPUT_DIR=/data
ENTRYPOINT ["/usr/local/bin/geox-sync"]
