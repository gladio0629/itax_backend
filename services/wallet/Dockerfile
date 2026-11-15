# Build konteksti: services/wallet (docker-compose: build: ./services/wallet)
FROM gradle:8.10-jdk21 AS build
WORKDIR /src
COPY settings.gradle.kts build.gradle.kts ./
COPY src ./src
RUN gradle bootJar --no-daemon -x test

FROM eclipse-temurin:21-jre-alpine
RUN adduser -D -u 10001 app
USER app
WORKDIR /app
COPY --from=build /src/build/libs/app.jar /app/app.jar
ENV PORT=8015
EXPOSE 8015
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=5 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${PORT}/healthz" || exit 1
ENTRYPOINT ["java", "-XX:MaxRAMPercentage=75", "-jar", "/app/app.jar"]
