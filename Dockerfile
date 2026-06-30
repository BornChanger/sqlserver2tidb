# syntax=docker/dockerfile:1

FROM golang:1.22-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 go build \
      -ldflags "-X github.com/BornChanger/sqlserver2tidb/internal/buildinfo.Version=${VERSION} -X github.com/BornChanger/sqlserver2tidb/internal/buildinfo.Commit=${COMMIT} -X github.com/BornChanger/sqlserver2tidb/internal/buildinfo.BuildDate=${BUILD_DATE}" \
      -o /out/sqlserver2tidb ./cmd/sqlserver2tidb \
    && CGO_ENABLED=0 go build \
      -ldflags "-X github.com/BornChanger/sqlserver2tidb/internal/buildinfo.Version=${VERSION} -X github.com/BornChanger/sqlserver2tidb/internal/buildinfo.Commit=${COMMIT} -X github.com/BornChanger/sqlserver2tidb/internal/buildinfo.BuildDate=${BUILD_DATE}" \
      -o /out/sqlserver2tidb-executor ./cmd/sqlserver2tidb-executor

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git gh \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system sqlserver2tidb \
    && useradd --system --gid sqlserver2tidb --home-dir /workspace --create-home sqlserver2tidb

COPY --from=build /out/sqlserver2tidb /usr/local/bin/sqlserver2tidb
COPY --from=build /out/sqlserver2tidb-executor /usr/local/bin/sqlserver2tidb-executor

WORKDIR /workspace
USER sqlserver2tidb

ENTRYPOINT ["sqlserver2tidb"]
CMD ["help"]
