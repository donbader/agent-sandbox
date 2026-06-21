FROM moby/buildkit:rootless
USER root
RUN apk add --no-cache ca-certificates su-exec
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
