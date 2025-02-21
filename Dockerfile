FROM golang:1.23-alpine AS builder
WORKDIR /app

# Copy module files first
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of your source code
COPY . .

# Ensure dependencies are tidy and pinned
RUN go mod tidy

# Build the binary, explicitly referencing the cmd/ directory
RUN CGO_ENABLED=0 go build -o volumescaler ./cmd

FROM alpine:3.17
WORKDIR /app
COPY --from=builder /app/volumescaler .
RUN chmod +x /app/volumescaler
ENTRYPOINT ["./volumescaler"]