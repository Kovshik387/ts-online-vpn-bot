FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/bot .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 bot
USER bot
COPY --from=build /out/bot /bot
ENTRYPOINT ["/bot"]
