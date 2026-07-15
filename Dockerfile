# Build stage
FROM golang:1.25-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main .

# Final stage
FROM alpine:latest

# Install ca-certificates and tzdata for HTTPS requests and timezone support
RUN apk update && apk --no-cache add ca-certificates tzdata

# Set timezone
ENV TZ=Asia/Ho_Chi_Minh

# Create app directory
WORKDIR /root/

# Ensure data/logs dirs exist (SQLite + price history); volumes mount over these
RUN mkdir -p /root/data /root/logs

# Copy the binary from builder stage
COPY --from=builder /app/main .

# Copy baked non-secret config (secrets come from env vars at runtime)
COPY --from=builder /app/config.docker.yaml ./config.yaml

# Expose port (if needed for webhooks)
EXPOSE 8080

# Command to run
CMD ["./main"]