# bbb-apps-akka (akka-bbb-apps) for BigBlueButton 4.1.
# Build context "src" = the bigbluebutton monorepo at the pinned commit (see images/build.sh).
ARG BUILDER_IMAGE=bigbluebutton/bbb-build:v4.1.x-release--2026-10-06-224206

FROM ${BUILDER_IMAGE} AS builder
ENV JAVA_TOOL_OPTIONS=-Dfile.encoding=UTF8
COPY --from=src bbb-common-message /src/bbb-common-message
RUN cd /src/bbb-common-message && sbt publishLocal
COPY --from=src akka-bbb-apps /src/akka-bbb-apps
RUN cd /src/akka-bbb-apps \
    && sbt universal:packageBin \
    && unzip -q target/universal/bbb-apps-akka-*.zip -d /opt \
    && mv /opt/bbb-apps-akka-* /opt/bbb-apps-akka

FROM eclipse-temurin:21-jre-noble
ARG SOURCE_SHA=unknown
LABEL org.opencontainers.image.source="https://github.com/imdt-claudiop/bbb-k8s" \
      org.opencontainers.image.description="BigBlueButton 4.1 akka-apps" \
      org.bigbluebutton.source-sha="${SOURCE_SHA}"
# Same UID/GID as every other image that will share BBB volumes (spec D2).
RUN groupadd -g 998 bigbluebutton \
    && useradd -m -u 998 -g 998 bigbluebutton \
    && mkdir -p /etc/bigbluebutton /usr/share/bigbluebutton/html5-client/private/config
COPY --from=builder --chown=998:998 /opt/bbb-apps-akka /usr/share/bbb-apps-akka
WORKDIR /usr/share/bbb-apps-akka
USER 998:998
# The start script carries -Dconfig.file=/etc/bigbluebutton/bbb-apps-akka.conf (akka-bbb-apps/build.sbt).
ENTRYPOINT ["/usr/share/bbb-apps-akka/bin/bbb-apps-akka"]
