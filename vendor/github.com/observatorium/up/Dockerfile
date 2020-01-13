FROM alpine

RUN apk add ca-certificates --no-cache

COPY up /usr/bin/up

ENTRYPOINT ["/usr/bin/up"]
