# CI builds the frontends and the binaries first (.gitlab-ci.yml); this only assembles them.
# The alpine base brings the CA bundle and the wget the compose healthcheck calls.
FROM alpine:3.22
ARG APP
ARG OCI_SOURCE
ARG OCI_REVISION
LABEL org.opencontainers.image.title="new-api-sub ${APP}" \
      org.opencontainers.image.source="$OCI_SOURCE" \
      org.opencontainers.image.revision="$OCI_REVISION" \
      org.opencontainers.image.licenses="AGPL-3.0-only"
COPY build/${APP} /app/server
COPY web/ui/LICENSE web/ui/NOTICE web/ui/THIRD-PARTY-LICENSES.md /app/
USER 10001:10001
ENTRYPOINT ["/app/server"]
