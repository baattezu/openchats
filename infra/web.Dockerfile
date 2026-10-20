FROM node:24-alpine AS build
WORKDIR /src/client
COPY client/package*.json ./
RUN npm ci
COPY client/ ./
COPY tokens.css /src/tokens.css
RUN npm run build
FROM caddy:2.10-alpine
COPY infra/Caddyfile /etc/caddy/Caddyfile
COPY --from=build /src/client/dist /srv
