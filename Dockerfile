# The builder runs natively and cross-compiles; CGO is off, so no emulation is needed.
FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.27.1-alpine3.24@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
COPY internal/ internal/

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags '-s -w' -o /webhook .

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3

COPY --from=build /webhook /usr/local/bin/webhook

USER 65532:65532

ENTRYPOINT ["/usr/local/bin/webhook"]
