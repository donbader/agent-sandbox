FROM moby/buildkit:latest AS buildkit

FROM alpine:3.20
RUN apk add --no-cache ca-certificates runc su-exec \
    && adduser -D -u 1000 buildkit \
    && mkdir -p /home/buildkit/.local/share/buildkit \
    && chown -R buildkit:buildkit /home/buildkit
COPY --from=buildkit /usr/bin/buildkitd /usr/local/bin/buildkitd
COPY --from=buildkit /usr/bin/buildctl /usr/local/bin/buildctl
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
