# Build context = repo root (needs go.mod + controller/). Build with:
#   docker build -f images/controller/Dockerfile -t mc/controller:dev .
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod ./
COPY controller/ ./controller/
RUN CGO_ENABLED=0 go build -o /controller ./controller

FROM gcr.io/distroless/static-debian12
COPY --from=build /controller /controller
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/controller"]
