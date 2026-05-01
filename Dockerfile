# Multi-stage parameterised build. One image per cmd/<plugin>.
# Build with: docker build --build-arg PLUGIN=<name> -t <tag> .
FROM golang:1.23-alpine AS builder
ARG PLUGIN
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY pkg ./pkg
COPY cmd ./cmd
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /out/plugin ./cmd/${PLUGIN}

# distroless/static gives us a CA bundle for HTTPS calls (mempalace, http-egress).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/plugin /plugin
USER nonroot
EXPOSE 8000
ENTRYPOINT ["/plugin"]
