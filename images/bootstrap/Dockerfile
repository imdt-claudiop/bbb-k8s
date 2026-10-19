# Database bootstrap for BigBlueButton 4.1: roles, databases and schemas.
# Replaces the postinst steps of bbb-graphql-server and bbb-shared-notes-server.
# Build context "src" = the bigbluebutton monorepo at the pinned commit (see images/build.sh).
FROM postgres:16
ARG SOURCE_SHA=unknown
LABEL org.opencontainers.image.source="https://github.com/imdt-claudiop/bbb-k8s" \
      org.opencontainers.image.description="BigBlueButton 4.1 database bootstrap" \
      org.bigbluebutton.source-sha="${SOURCE_SHA}"
COPY --from=src bbb-graphql-server/bbb_schema.sql /bootstrap/bbb_schema.sql
COPY --from=src bbb-shared-notes-server/blocknote_schema.sql /bootstrap/blocknote_schema.sql
COPY bootstrap.sh /bootstrap/bootstrap.sh
USER postgres
ENTRYPOINT ["/bootstrap/bootstrap.sh"]
