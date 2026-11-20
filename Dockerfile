# Build tibi from source and run it with the HEIC converter installed.
#
#   docker build -t tibi .
#   docker run -d -p 8070:8070 -v tibi-data:/data -e TIBI_BASE_URL=https://tibi.example.com tibi
#   docker exec -it <container> tibi admin token create --name me --scopes upload,delete,read_private
FROM golang:1.26-trixie AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/baojie/tibi/internal/app.Version=${VERSION}" -o /out/tibi ./cmd/tibi

FROM debian:trixie-slim
# libvips-tools + the libde265 plugin: HEIC → JPEG; libwebp7: fast thumbnails.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata libvips-tools libheif-plugin-libde265 libwebp7 \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10001 --home-dir /data --shell /usr/sbin/nologin tibi \
 && install -d -o tibi -g tibi /data
COPY --from=build /out/tibi /usr/bin/tibi
COPY --from=build /src/LICENSE /usr/share/doc/tibi/LICENSE
LABEL org.opencontainers.image.licenses="AGPL-3.0-only" org.opencontainers.image.title="tibi"
USER tibi
ENV TIBI_DATA_DIR=/data TIBI_LISTEN=0.0.0.0:8070 TIBI_BASE_URL=http://localhost:8070
VOLUME /data
EXPOSE 8070
ENTRYPOINT ["/usr/bin/tibi"]
CMD ["serve"]
