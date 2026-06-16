ARG JRE_TAG=21-jre
FROM eclipse-temurin:${JRE_TAG}
ARG VELOCITY_URL
WORKDIR /app
ADD ${VELOCITY_URL} /app/velocity.jar
COPY velocity.toml /app/velocity.toml
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh && useradd -r mc && chown -R mc /app
USER mc
EXPOSE 25565
ENTRYPOINT ["/entrypoint.sh"]
