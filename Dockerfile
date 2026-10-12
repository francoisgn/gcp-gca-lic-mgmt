# syntax=docker/dockerfile:1
# -----------------------------------------------------------------------------
# Gemini Code Assist license portal — single static binary, distroless image.
# The same image runs the "web" and the "worker" Cloud Run services
# (APP_MODE=web | worker).
# -----------------------------------------------------------------------------
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/gcalm ./cmd/gcalm \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/edsim ./cmd/edsim

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/gcalm /app/gcalm
COPY --from=build /out/edsim /app/edsim
COPY config/ /app/config/
ENV ROLES_FILE=/app/config/roles.yaml \
    POLICY_FILE=/app/config/policy.yaml \
    PORT=8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/gcalm"]
