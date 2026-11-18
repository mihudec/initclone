FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" -o /out/initclone .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=build /out/initclone /usr/local/bin/initclone
# Root is intentional: applying the requested UID/GID to mounted volumes requires it.
USER 0:0
ENTRYPOINT ["/usr/local/bin/initclone"]
