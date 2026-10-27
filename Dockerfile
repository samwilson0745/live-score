# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ingest ./cmd/ingest

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /out/ingest /
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]
