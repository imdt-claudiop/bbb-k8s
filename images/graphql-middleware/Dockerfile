# bbb-graphql-middleware for BigBlueButton 4.1.
# Build context "src" = the bigbluebutton monorepo at the pinned commit (see images/build.sh).
ARG BUILDER_IMAGE=bigbluebutton/bbb-build:v4.1.x-release--2026-10-06-224206

FROM ${BUILDER_IMAGE} AS builder
COPY --from=src bbb-graphql-middleware /src/bbb-graphql-middleware
# Same build as build/packages-template/bbb-graphql-middleware/build.sh.
RUN cd /src/bbb-graphql-middleware \
    && CGO_ENABLED=0 go build -o /out/bbb-graphql-middleware cmd/bbb-graphql-middleware/main.go

FROM ubuntu:24.04
ARG SOURCE_SHA=unknown
LABEL org.opencontainers.image.source="https://github.com/imdt-claudiop/bbb-k8s" \
      org.opencontainers.image.description="BigBlueButton 4.1 graphql-middleware" \
      org.bigbluebutton.source-sha="${SOURCE_SHA}"
RUN groupadd -g 998 bigbluebutton && useradd -m -u 998 -g 998 bigbluebutton \
    && mkdir -p /usr/share/bbb-graphql-middleware /etc/bigbluebutton
COPY --from=builder /out/bbb-graphql-middleware /usr/bin/bbb-graphql-middleware
# Defaults; the chart mounts /etc/bigbluebutton/bbb-graphql-middleware.yml as the override.
COPY --from=src bbb-graphql-middleware/config/config.yml /usr/share/bbb-graphql-middleware/config.yml
USER 998:998
ENTRYPOINT ["/usr/bin/bbb-graphql-middleware"]
