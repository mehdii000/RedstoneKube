ARG JRE_TAG=21-jre
FROM eclipse-temurin:${JRE_TAG}
ARG ASP_URL
ARG ASP_PLUGIN_URL
RUN useradd -r mc && mkdir -p /server/plugins /worlds /opt/config && chown -R mc /server /worlds
ADD ${ASP_URL} /opt/server.jar
ADD ${ASP_PLUGIN_URL} /server/plugins/asp-plugin.jar
COPY config/ /opt/config/
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh && chmod 0644 /opt/server.jar /server/plugins/asp-plugin.jar \
 && chown -R mc /server/plugins \
 && echo "eula=true" > /server/eula.txt && chown mc /server/eula.txt
USER mc
WORKDIR /server
EXPOSE 25565
ENTRYPOINT ["/entrypoint.sh"]
