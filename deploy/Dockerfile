# Multi-stage build of the LockLens server image: web UI, Go binaries, and a
# small runtime image running as a non-root user.

# The web and Go stages run on the build machine's architecture (no emulation);
# Go cross-compiles for the target platform.
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json web/.npmrc ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=web /src/web/dist/ ./internal/webui/dist/
ARG VERSION=dev
ARG COMMIT=""
ARG TARGETOS=linux
ARG TARGETARCH
RUN export GOOS=$TARGETOS GOARCH=$TARGETARCH; CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/locklens/locklens/internal/buildinfo.Version=${VERSION} -X github.com/locklens/locklens/internal/buildinfo.Commit=${COMMIT}" \
      -o /out/locklens ./cmd/locklens \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/locklens-lab ./cmd/locklens-lab

FROM alpine:3.22
RUN adduser -D -H -u 10001 locklens
COPY --from=build /out/locklens /usr/local/bin/locklens
COPY --from=build /out/locklens-lab /usr/local/bin/locklens-lab
USER locklens
EXPOSE 8080
ENTRYPOINT ["locklens"]
CMD ["server"]
