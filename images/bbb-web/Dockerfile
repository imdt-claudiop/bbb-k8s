# bbb-web (bigbluebutton-web) for BigBlueButton 4.1.
# Build context "src" = the bigbluebutton monorepo at the pinned commit (see images/build.sh).
ARG BUILDER_IMAGE=bigbluebutton/bbb-build:v4.1.x-release--2026-10-06-224206

FROM ${BUILDER_IMAGE} AS builder
ENV JAVA_TOOL_OPTIONS=-Dfile.encoding=UTF8
# Same order as build/packages-template/bbb-web/build.sh: common-message, common-web, the war,
# pres-checker. The sbt projects publish to ~/.m2, where gradle (mavenLocal) finds them.
COPY --from=src bbb-common-message /src/bbb-common-message
RUN cd /src/bbb-common-message && sbt publish publishLocal
COPY --from=src bbb-common-web /src/bbb-common-web
RUN cd /src/bbb-common-web && sbt update publish publishLocal
COPY --from=src bigbluebutton-web /src/bigbluebutton-web
RUN cd /src/bigbluebutton-web \
    && ./gradlew resolveDeps \
    && ./gradlew assemble \
    && mkdir -p /out/bbb-web && cd /out/bbb-web && jar -xf /src/bigbluebutton-web/build/libs/bigbluebutton-0.10.0.war
RUN cd /src/bigbluebutton-web/pres-checker \
    && gradle resolveDeps && gradle jar \
    && mkdir -p /out/prescheck/lib \
    && cp lib/* /out/prescheck/lib/ \
    && cp build/libs/bbb-pres-check-0.0.1.jar /out/prescheck/lib/ \
    && cp run.sh /out/prescheck/prescheck.sh

FROM eclipse-temurin:21-jre-noble
ARG SOURCE_SHA=unknown
LABEL org.opencontainers.image.source="https://github.com/imdt-claudiop/bbb-k8s" \
      org.opencontainers.image.description="BigBlueButton 4.1 bbb-web" \
      org.bigbluebutton.source-sha="${SOURCE_SHA}"
# Runtime dependencies of the bbb-web package (build/packages-template/bbb-web/opts-noble.sh),
# minus redis-server (its own pod), the JDK (this JRE) and bbb-libreoffice-docker (Office
# conversion stays out of phase 1, spec L5). xpdf-utils is the transitional name of poppler-utils.
RUN apt-get update && apt-get install -y --no-install-recommends \
        zip unzip imagemagick libmagickcore-6.q16-7-extra poppler-utils psmisc \
        fonts-crosextra-carlito fonts-crosextra-caladea fonts-noto file potrace ghostscript \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -g 998 bigbluebutton && useradd -m -u 998 -g 998 bigbluebutton
COPY --from=builder --chown=998:998 /out/bbb-web /usr/share/bbb-web
COPY --from=builder /out/prescheck /usr/share/prescheck
# Blank slides installed by the bbb-config package (bigbluebutton-config/slides).
COPY --from=src bigbluebutton-config/slides/ /usr/share/bigbluebutton/blank/
COPY run-in-systemd.sh /usr/share/bbb-web/run-in-systemd.sh
COPY entrypoint.sh /entrypoint.sh
RUN chmod 755 /usr/share/prescheck/prescheck.sh /usr/share/bbb-web/run-in-systemd.sh /entrypoint.sh \
    && mkdir -p /etc/bigbluebutton /usr/share/bigbluebutton/html5-client/private/config \
    && chown 998:998 /etc/bigbluebutton
WORKDIR /usr/share/bbb-web
USER 998:998
ENTRYPOINT ["/entrypoint.sh"]
