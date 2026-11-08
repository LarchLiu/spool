FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /spool-relay ./cmd/spool-relay

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 relay && mkdir /data && chown relay:relay /data && chmod 700 /data
USER relay
COPY --from=build /spool-relay /usr/local/bin/spool-relay
EXPOSE 8788
ENTRYPOINT ["spool-relay"]
CMD ["-listen", "0.0.0.0:8788", "-data-dir", "/data"]
