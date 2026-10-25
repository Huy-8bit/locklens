# LockLens frontend image: the built React UI served by nginx (unprivileged),
# which also proxies the API (/api/) to the backend so the browser talks to a
# single origin.
#
#   docker build -t locklens-frontend web/
#   docker run -e LOCKLENS_BACKEND_URL=http://backend:8080 -p 8080:8080 locklens-frontend

# The UI is plain static files; build them on the build machine's architecture.
FROM --platform=$BUILDPLATFORM node:24-alpine AS build
WORKDIR /src
COPY package.json package-lock.json .npmrc ./
RUN npm ci --no-audit --no-fund
COPY . .
RUN npm run build

FROM nginxinc/nginx-unprivileged:1.27-alpine
# The entrypoint renders /etc/nginx/templates/*.template with the variables
# below (only those, so nginx's own $variables are left alone).
ENV LOCKLENS_BACKEND_URL=http://backend:8080 \
    NGINX_ENVSUBST_FILTER=^LOCKLENS_
COPY nginx/default.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /src/dist /usr/share/nginx/html
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --retries=6 CMD wget -q -O /dev/null http://127.0.0.1:8080/frontend-healthz || exit 1
