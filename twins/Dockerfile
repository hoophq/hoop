# One image for every twin, the router and the PKI step.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/twins ./cmd/twins

FROM alpine:3.20
RUN apk add --no-cache curl
COPY --from=build /out/twins /usr/local/bin/twins
ENTRYPOINT ["/usr/local/bin/twins"]
