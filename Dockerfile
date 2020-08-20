# Dockerfile References: https://docs.docker.com/engine/reference/builder/

# Start from the latest golang base image
FROM golang:latest

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

# Expose port 11101, 11102 to the outside world
EXPOSE 11101
EXPOSE 11102

# Command to run the executable
ENTRYPOINT ["/app/bin/yorkie", "agent"]
