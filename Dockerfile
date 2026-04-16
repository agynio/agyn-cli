ARG BUF_VERSION=1.66.1

FROM golang:1.24-alpine AS generate
ARG BUF_VERSION
WORKDIR /src
RUN apk add --no-cache curl
RUN curl -sSL \
      "https://github.com/bufbuild/buf/releases/download/v${BUF_VERSION}/buf-$(uname -s)-$(uname -m)" \
      -o /usr/local/bin/buf && \
    chmod +x /usr/local/bin/buf
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN buf generate

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY --from=generate /src .
ARG TARGETOS TARGETARCH
ENV CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH
RUN go build -trimpath -ldflags "-s -w" -o /out/agyn ./cmd/agyn

FROM alpine:3.21
COPY --from=build /out/agyn /usr/local/bin/agyn
RUN addgroup -g 10001 -S app && adduser -u 10001 -S app -G app
USER 10001
ENTRYPOINT ["agyn"]
