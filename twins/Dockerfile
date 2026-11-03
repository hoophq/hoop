# One image for every twin, the router and the PKI step. The compose stack
# in scenario/compose.yml builds it.
FROM golang:1.26.10-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOWORK=off go build -ldflags "-s -w" -o /out/twins ./cmd/twins

FROM alpine:3.20
COPY --from=build /out/twins /usr/local/bin/twins
ENTRYPOINT ["/usr/local/bin/twins"]
