# Use the official Go image as the base image
FROM golang:1.18-alpine AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy go.mod and go.sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the application
RUN go build -o darkmax darkmax.go

# Use a smaller base image for the final stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates

# Set the working directory
WORKDIR /root/

# Copy the binary from the builder stage
COPY --from=builder /app/darkmax .

# Expose port if needed (for webhook, but this bot uses polling)
# EXPOSE 8080

# Command to run the application
CMD ["./darkmax"]