# Static binaries are built locally for the Docker host architecture.
# Copy public CA roots only; credentials never enter the image or build context.
FROM alpine:3.22 AS certificates
FROM scratch
COPY --from=certificates /etc/ssl/cert.pem /etc/ssl/cert.pem
COPY bin/webchat-server /webchat-server
COPY bin/migrate /migrate
USER 65532:65532
ENTRYPOINT ["/webchat-server"]
