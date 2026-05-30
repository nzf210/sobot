FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o backend cmd/main.go

FROM alpine:latest
WORKDIR /app
RUN apk --no-cache add ca-certificates

COPY --from=builder /app/backend .

# Expose backend port
EXPOSE 8080

CMD ["./backend"]
