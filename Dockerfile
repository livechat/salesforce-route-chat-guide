FROM golang:1.27-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/salesforce-router ./cmd/server

FROM alpine:3.24

RUN apk add --no-cache ca-certificates
COPY --from=build /out/salesforce-router /usr/local/bin/salesforce-router

USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/salesforce-router"]
