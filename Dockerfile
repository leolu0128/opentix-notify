FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /app ./cmd/app

FROM alpine:3.20
# ca-certificates:HTTPS 出站(OPENTIX/Discord);tzdata:通知顯示 Asia/Taipei
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /app /app
ENTRYPOINT ["/app"]
CMD ["serve"]
