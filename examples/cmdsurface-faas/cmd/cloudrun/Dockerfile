# Multi-stage build for Cloud Run.
#
# Build:        docker build -t cmdsurface-faas-cloudrun \
#                   -f examples/cmdsurface-faas/cmd/cloudrun/Dockerfile .
# Run locally:  docker run --rm -p 8080:8080 cmdsurface-faas-cloudrun
# Deploy:       gcloud run deploy cmdsurface-faas-demo \
#                   --source . --region us-central1

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
    -o /app ./examples/cmdsurface-faas/cmd/cloudrun

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /app /app
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/app"]
