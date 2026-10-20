# Front nginx for BigBlueButton 4.1: html5 client, learning dashboard, default assets and the
# package nginx fragments. Build context "src" = the bigbluebutton monorepo at the pinned commit.
ARG BUILDER_IMAGE=bigbluebutton/bbb-build:v4.1.x-release--2026-10-06-224206

FROM ${BUILDER_IMAGE} AS html5
ARG SOURCE_SHA=unknown
COPY --from=src bigbluebutton-html5 /src/bigbluebutton-html5
# Same steps as build/packages-template/bbb-html5/build.sh.
RUN cd /src/bigbluebutton-html5 \
    && CI=true npm ci \
    && DISABLE_ESLINT_PLUGIN=true npm run build-safari && npm run build
RUN cd /src/bigbluebutton-html5/dist \
    && HASH=$(ls | grep -Eo 'bundle\.[a-f0-9]{20}\.js' | head -n 1 | grep -Eo '[a-f0-9]{20}') \
    && if [ -z "$HASH" ]; then echo "Bundle hash not found."; else \
         for FILE in *.safari.js *.safari.js.map; do \
           case "$FILE" in *"$HASH"*) continue ;; esac; \
           PREFIX="${FILE%%.safari.js*}"; SUFFIX="${FILE#*.safari.js}"; \
           mv "$FILE" "${PREFIX}.${HASH}.safari.js${SUFFIX}"; \
         done; fi \
    && sed -i "s/?v=VERSION/?v=${SOURCE_SHA}/g" index.html stylesheets/fonts.css

FROM ${BUILDER_IMAGE} AS dashboard
COPY --from=src bbb-learning-dashboard /src/bbb-learning-dashboard
# Same steps as build/packages-template/bbb-learning-dashboard/build.sh.
RUN cd /src/bbb-learning-dashboard && npm ci && npm run build

FROM ${BUILDER_IMAGE} AS playback
COPY --from=src bbb-playback /src/bbb-playback
# Same steps as build/packages-template/bbb-playback/build.sh (the player of the presentation
# recording format, served at /playback/presentation/2.3). The package's jq edit of
# src/config.json is skipped: that file no longer exists at the pinned tag (the config is
# src/config.js), so the package step changes nothing either.
RUN cd /src/bbb-playback \
    && npm install && npm run-script build \
    && grep '"version":' package.json | sed -e 's|.* "||g' -e 's|".*||g' > build/bbb-playback-version

FROM ${BUILDER_IMAGE} AS fragments
# The package nginx fragments of the pinned commit, with the loopback upstreams of a single-host
# install replaced by the Service names of this chart (the 8185 Hasura load balancer of
# bbb-graphql-middleware becomes the hasura Service; LiveKit and the SFU are their Services).
COPY --from=src bigbluebutton-web/bbb-web.nginx /f/web.nginx
COPY --from=src bigbluebutton-web/nginx-confs/presentation-slides.nginx /f/presentation-slides.nginx
COPY --from=src build/packages-template/bbb-html5/bbb-html5.nginx /f/bbb-html5.nginx
COPY --from=src build/packages-template/bbb-html5/bbb-html5.nginx.static /f/bbb-html5.nginx.static
COPY --from=src build/packages-template/bbb-graphql-middleware/graphql.nginx /f/graphql.nginx
COPY --from=src build/packages-template/bbb-graphql-middleware/bbb-graphql-client-settings-cache.conf /f/conf.d/bbb-graphql-client-settings-cache.conf
COPY --from=src bbb-learning-dashboard/learning-dashboard.nginx /f/learning-dashboard.nginx
COPY --from=src build/packages-template/bbb-config/plugins-assets-cors.nginx /f/plugins-assets-cors.nginx
COPY --from=src build/packages-template/bbb-livekit/livekit.nginx /f/livekit.nginx
COPY --from=src build/packages-template/bbb-webrtc-sfu/webrtc-sfu.nginx /f/webrtc-sfu.nginx
COPY --from=src build/packages-template/bbb-playback/playback.nginx /f/playback.nginx
COPY --from=src record-and-playback/presentation/scripts/presentation.nginx /f/presentation.nginx
RUN sed -i -e 's|http://127.0.0.1:8090|http://bbb-web:8090|g' \
           -e 's|http://127.0.0.1:8378|http://gql-middleware:8378|g' \
           -e 's|http://127.0.0.1:8185|http://hasura:8085|g' \
           -e 's|http://127.0.0.1:7880|http://livekit:7880|g' \
           -e 's|http://127.0.0.1:3008|http://bbb-webrtc-sfu:3008|g' /f/*.nginx \
    && ! grep -n '^[^#]*127\.0\.0\.1' /f/*.nginx

FROM nginxinc/nginx-unprivileged:1.29
ARG SOURCE_SHA=unknown
LABEL org.opencontainers.image.source="https://github.com/imdt-claudiop/bbb-k8s" \
      org.opencontainers.image.description="BigBlueButton 4.1 front nginx with html5 client" \
      org.bigbluebutton.source-sha="${SOURCE_SHA}"
USER root
COPY --from=html5 /src/bigbluebutton-html5/dist /usr/share/bigbluebutton/html5-client
COPY --from=dashboard /src/bbb-learning-dashboard/build /opt/learning-dashboard
# Installed by the packages under /var/bigbluebutton, i.e. inside the bbb-data volume: an init
# container copies them there.
COPY --from=playback /src/bbb-playback/build /opt/playback/presentation/2.3
COPY --from=src bigbluebutton-config/assets /var/www/bigbluebutton-default/assets
COPY --from=fragments /f/conf.d/ /etc/nginx/conf.d/
COPY --from=fragments /f/*.nginx /f/bbb-html5.nginx.static /usr/share/bigbluebutton/nginx/
# bbb-config's postinst raises worker_connections to 10000; the server block comes from the chart.
RUN sed -i 's/worker_connections .*/worker_connections 10000;/' /etc/nginx/nginx.conf \
    && rm -f /etc/nginx/conf.d/default.conf \
    && chmod -R a+rX /usr/share/bigbluebutton /var/www/bigbluebutton-default /opt/learning-dashboard /opt/playback
USER 998:998
