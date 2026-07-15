FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /app ./cmd/app

FROM alpine:3.22
# ca-certificates:HTTPS 出站(OPENTIX/Discord);tzdata:通知顯示 Asia/Taipei
RUN apk add --no-cache ca-certificates tzdata
RUN adduser -D -u 10001 app
COPY --from=build /app /app
USER app
EXPOSE 8080
ENTRYPOINT ["/app"]
CMD ["serve"]
