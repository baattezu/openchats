FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY api/go.mod api/go.sum ./
RUN go mod download
COPY api/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /service ./cmd/service
FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app && mkdir -p /data/media && chown -R app:app /data
COPY --from=build /service /service
USER app
EXPOSE 8080
ENTRYPOINT ["/service"]
