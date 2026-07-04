# syntax=docker/dockerfile:1

# Stage 1: build the SPA. Runs on the build platform — the output is static
# files, so emulating the target arch would only buy QEMU slowness and
# platform-specific npm binary flakes (lightningcss et al.).
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-fund --no-audit
COPY web/ .
RUN npm run build

# Stage 2: cross-compile the Go binary with the SPA embedded. CGO is off, so
# GOOS/GOARCH is all a target needs — again no emulation.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.* ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -tags embedspa \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/vellum ./cmd/vellum

# Stage 3: minimal runtime
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/vellum /vellum
ENV PORT=8080 \
    VELLUM_VAULT_PATH=/vault
EXPOSE 8080
# distroless has no shell/curl; the binary probes itself (see -healthcheck flag)
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s \
    CMD ["/vellum", "-healthcheck"]
ENTRYPOINT ["/vellum"]
