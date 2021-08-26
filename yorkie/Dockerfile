# Dockerfile References: https://docs.docker.com/engine/reference/builder/

# Stage 1: build binary
# Start from the latest golang base image
FROM golang:1-buster AS builder

# Add Maintainer Info
LABEL maintainer="hackerwins <susukang98@gmail.com>"

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Copy the source from the current directory to the Working Directory inside the container
COPY . .

# Build the yorkie
RUN make build

# Stage 2: get ca-certificates
FROM alpine:3 as certs
RUN apk --no-cache add ca-certificates

# Stage 3: copy ca-certificates and binary
FROM debian:buster-slim

# Get and place ca-certificates to /etc/ssl/certs
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# Get and place binary to /bin
COPY --from=builder /app/bin/yorkie /bin/

# Expose port 11101, 11102 to the outside world
EXPOSE 11101 11102

# Define default entrypoint.
ENTRYPOINT ["yorkie"]
