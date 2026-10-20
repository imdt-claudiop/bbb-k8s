# Hasura GraphQL Engine for BigBlueButton 4.1 (bbb-graphql-server).
#
# The bbb-graphql-server package ships the Community Edition binary published in
# iMDT/hasura-graphql-engine (build/packages-template/bbb-graphql-server/build.sh), while the
# official hasura/graphql-engine image ships the Pro Edition binary. The official image only
# provides the runtime libraries here; the engine is the same binary the package installs.
ARG HASURA_VERSION=v2.49.3

FROM alpine/git:latest AS engine
ARG HASURA_VERSION
# sha256 of the concatenated parts on the v2.49.3 branch, checked so a moved branch fails the build.
ARG HASURA_CE_SHA256=06320f1c93944ec9148721b5cf563fe8a733e720fef2bbfbc60f2c99bca7c585
RUN git clone -q --depth 1 --branch "${HASURA_VERSION}" https://github.com/iMDT/hasura-graphql-engine.git /h \
    && cat /h/hasura-graphql.part-a* > /graphql-engine \
    && echo "${HASURA_CE_SHA256}  /graphql-engine" | sha256sum -c - \
    && chmod 755 /graphql-engine

FROM hasura/graphql-engine:${HASURA_VERSION}
ARG SOURCE_SHA=unknown
LABEL org.opencontainers.image.source="https://github.com/imdt-claudiop/bbb-k8s" \
      org.opencontainers.image.description="BigBlueButton 4.1 Hasura GraphQL Engine (CE binary)" \
      org.bigbluebutton.source-sha="${SOURCE_SHA}"
COPY --from=engine /graphql-engine /usr/bin/graphql-engine
RUN groupadd -g 998 bigbluebutton && useradd -m -u 998 -g 998 bigbluebutton
USER 998:998
ENTRYPOINT ["/usr/bin/graphql-engine"]
CMD ["serve"]
