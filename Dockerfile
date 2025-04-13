cAS builder
WORKDIR /app

# Copy module files first
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of your source code
COPY . .

# Ensure dependencies are tidy and pinned
RUN go mod tidy

# Build the binary, outputting it with a new name (volumescaler-bin) to avoid conflicts.
RUN CGO_ENABLED=0 go build -o volumescaler-bin ./cmd

# Debug: list the files in /app to verify the binary exists
RUN ls -l /app

FROM alpine:3.17
WORKDIR /app

# Copy the built binary from the builder stage using the new name
COPY --from=builder /app/volumescaler-bin /app/volumescaler-bin

# Ensure the binary is executable
RUN chmod +x /app/volumescaler-bin

# Set the ENTRYPOINT to run the binary
ENTRYPOINT ["/app/volumescaler-bin"]
