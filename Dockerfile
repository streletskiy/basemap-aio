FROM golang:1.23-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./

RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/basemapctl ./cmd/basemapctl

FROM debian:bookworm-slim

RUN apt-get update \
	&& apt-get install -y --no-install-recommends aria2 ca-certificates \
	&& rm -rf /var/lib/apt/lists/* \
	&& useradd --system --create-home --home-dir /home/basemap --shell /usr/sbin/nologin basemap

ENV HOME=/home/basemap

COPY --from=build /out/basemapctl /basemapctl

USER basemap

ENTRYPOINT ["/basemapctl"]
