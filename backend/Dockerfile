# LockLens backend image: the API server and collectors (`locklens server`),
# the CLI, and the lab scenario runner. No web UI: that is the frontend image.
#
#   docker build -t locklens-backend backend/

# Go cross-compiles on the build machine's architecture (no emulation).
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=""
ARG TARGETOS=linux
ARG TARGETARCH
RUN export GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0; \
    go build -trimpath \
      -ldflags "-s -w -X github.com/locklens/locklens/internal/buildinfo.Version=${VERSION} -X github.com/locklens/locklens/internal/buildinfo.Commit=${COMMIT}" \
      -o /out/locklens ./cmd/locklens \
 && go build -trimpath -ldflags "-s -w" -o /out/locklens-lab ./cmd/locklens-lab

FROM alpine:3.22
RUN adduser -D -H -u 10001 locklens
COPY --from=build /out/locklens /usr/local/bin/locklens
COPY --from=build /out/locklens-lab /usr/local/bin/locklens-lab
USER locklens
# Listen on all interfaces inside the container; publish the port only to
# the frontend's network.
ENV LOCKLENS_LISTEN=0.0.0.0:8080
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --retries=6 CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["locklens"]
CMD ["server"]
