# LockLens backend image: the API server and collectors (`locklens server`),
# the CLI, and the lab scenario runner. No web UI: that is the frontend image.
#
#   docker build -t locklens-backend backend/

# The server embeds PostgreSQL's SQL parser (libpg_query, C code), so it is
# built with cgo and linked statically against musl. cgo does not
# cross-compile without a cross toolchain, so the build stage runs on the
# target platform (emulated in multi-platform builds).
FROM golang:1.26-alpine AS build
RUN apk add --no-cache build-base
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=""
RUN CGO_ENABLED=1 go build -trimpath \
      -ldflags "-s -w -linkmode external -extldflags -static -X github.com/locklens/locklens/internal/lib/buildinfo.Version=${VERSION} -X github.com/locklens/locklens/internal/lib/buildinfo.Commit=${COMMIT}" \
      -o /out/locklens ./cmd/locklens \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/locklens-lab ./cmd/locklens-lab

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
