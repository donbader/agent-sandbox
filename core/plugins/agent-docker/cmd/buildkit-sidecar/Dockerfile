FROM moby/buildkit:latest AS buildkit

FROM alpine:3.20
RUN apk add --no-cache ca-certificates runc jq \
    && mkdir -p /var/lib/buildkit
COPY --from=buildkit /usr/bin/buildkitd /usr/local/bin/buildkitd
COPY --from=buildkit /usr/bin/buildctl /usr/local/bin/buildctl
COPY runc-wrapper.sh /usr/local/bin/runc-wrapper
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh /usr/local/bin/runc-wrapper
ENTRYPOINT ["/entrypoint.sh"]
