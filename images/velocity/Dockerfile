ARG JRE_TAG=21-jre
FROM eclipse-temurin:${JRE_TAG}
ARG VELOCITY_URL
WORKDIR /app
ADD ${VELOCITY_URL} /app/velocity.jar
COPY velocity.toml /app/velocity.toml
COPY velocity-register.jar /app/plugins/velocity-register.jar
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh && chmod 0644 /app/plugins/velocity-register.jar \
 && useradd -r mc && chown -R mc /app
USER mc
EXPOSE 25565 8080
ENTRYPOINT ["/entrypoint.sh"]
