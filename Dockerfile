FROM golang:1.23-alpine AS builder
WORKDIR /app

# Copy module files first
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of your source code
COPY . .

# Ensure dependencies are tidy and pinned
RUN go mod tidy

# Build a static binary so we can run on a distroless/scratch-style base.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o volumescaler-bin ./cmd

FROM alpine:3.17
WORKDIR /app

# Run as a non-root user to satisfy runAsNonRoot in the Pod securityContext.
RUN addgroup -S volumescaler && adduser -S -G volumescaler -u 65532 volumescaler

# Copy the built binary from the builder stage
COPY --from=builder /app/volumescaler-bin /app/volumescaler-bin
RUN chmod +x /app/volumescaler-bin

USER 65532:65532

# Health server port (must match HEALTH_ADDR / probe ports in the Deployment).
EXPOSE 8081

ENTRYPOINT ["/app/volumescaler-bin"]
