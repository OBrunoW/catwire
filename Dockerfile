FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/catwire ./cmd/catwire

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates ffmpeg \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10001 --create-home catwire \
 && mkdir -p /data/media && chown -R catwire:catwire /data
COPY --from=build /out/catwire /usr/local/bin/catwire
USER catwire
ENV PORT=5001 MEDIA_DIR=/data/media
EXPOSE 5001
ENTRYPOINT ["/usr/local/bin/catwire"]
