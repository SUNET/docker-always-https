# syntax=docker/dockerfile:1

# ---- build -------------------------------------------------------------------
FROM golang:1.27 AS build

ARG VERSION=dev
WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/always-https .

# ---- runtime -----------------------------------------------------------------
FROM scratch

# CA bundle for an https ACME_URL. Kept outside /etc/ssl because that path is
# a volume (the host certificate tree) and would hide it.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /ca-certificates.crt
ENV SSL_CERT_FILE=/ca-certificates.crt

COPY --from=build /out/always-https /always-https

# Non-root. Docker allows binding <1024 inside the container network
# namespace (net.ipv4.ip_unprivileged_port_start=0). With --network host use
# HTTP_ADDR/HTTPS_ADDR or run with --user 0.
USER 65532:65532

EXPOSE 80
EXPOSE 443
VOLUME /etc/ssl

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/always-https", "-healthcheck"]

ENTRYPOINT ["/always-https"]
