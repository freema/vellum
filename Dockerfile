# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.* ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/vellum ./cmd/vellum

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/vellum /vellum
ENV PORT=8080 \
    VELLUM_VAULT_PATH=/vault
EXPOSE 8080
# distroless has no shell/curl; the binary probes itself (see -healthcheck flag)
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s \
    CMD ["/vellum", "-healthcheck"]
ENTRYPOINT ["/vellum"]
