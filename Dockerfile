FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/mdfly-server ./cmd/mdfly-server

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S mdfly \
    && adduser -S -G mdfly mdfly
COPY --from=builder /bin/mdfly-server /bin/mdfly-server
RUN chown mdfly:mdfly /bin/mdfly-server
USER mdfly
ENTRYPOINT ["/bin/mdfly-server"]
