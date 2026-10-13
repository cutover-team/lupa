ARG REGISTRY=docker.io
FROM ${REGISTRY}/library/golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /lupa ./cmd/lupa

FROM ${REGISTRY}/library/alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 lupa && mkdir -p /data && chown lupa:lupa /data
COPY --from=build /lupa /usr/local/bin/lupa
USER lupa
VOLUME ["/data"]
ENV LUPA_ADDR=:8080 LUPA_DATA_DIR=/data
EXPOSE 8080
ENTRYPOINT ["lupa"]
