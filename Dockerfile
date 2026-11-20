# syntax=docker/dockerfile:1

# Build stage: static binaries for the API, the seed tool and goose (migrations).
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed \
 && CGO_ENABLED=0 GOBIN=/out go install github.com/pressly/goose/v3/cmd/goose@v3.30.0

# Runtime stage: no shell, no package manager, non-root user (PRD 6).
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/ /app/
COPY db/migrations /app/db/migrations
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/api"]
