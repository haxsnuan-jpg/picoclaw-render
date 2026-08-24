# PicoClaw deployment for Render (free-tier friendly).
#
# We base on the official prebuilt single-binary image (sipeed/picoclaw:latest)
# instead of compiling from source. This keeps the build tiny and fast and
# avoids the heavy Docker build that caused the previous "bad gateway" on
# Hermes. The startup wrapper (run.go) generates config.json from Render env
# vars, supervises `picoclaw gateway`, and serves $PORT for health checks
# while reverse-proxying bot webhooks to the gateway.

FROM golang:1.23-alpine AS build
WORKDIR /src
COPY run.go .
# Fully static binary (no libc dependency) so it runs in the picoclaw base
# image, which may not ship a dynamic loader. CGO_ENABLED=0 is required.
ENV CGO_ENABLED=0
RUN go build -o /run run.go

FROM sipeed/picoclaw:latest
COPY --from=build --chmod=0755 /run /usr/local/bin/run
# The picoclaw binary and its PATH/ENV are inherited from the base image.
EXPOSE 10000
ENTRYPOINT ["/usr/local/bin/run"]
