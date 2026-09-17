FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/tempmail ./cmd/tempmail

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/tempmail /app/tempmail
COPY web /app/web
RUN mkdir -p /app/data/attachments
EXPOSE 25 8080
ENTRYPOINT ["/app/tempmail"]
