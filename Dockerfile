# Stage 1: Build the Go binary
FROM golang:1.23-alpine AS builder

WORKDIR /build

# Copy go mod files
COPY merge/go.mod merge/go.sum ./
RUN go mod download

# Copy source code
COPY merge/ ./

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o gtfs-merge cmd/gtfs-merge/main.go

# Stage 2: Runtime image
FROM eclipse-temurin:17-jre

ARG JAR_VERSION=9.0.1
ENV JAR_VERSION=${JAR_VERSION}

RUN apt-get update && \
    apt-get install -y \
    bash \
    curl \
    unzip \
    jq \
    zip && \
    rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /app

# Copy Go binary from builder
COPY --from=builder /build/gtfs-merge /app/gtfs-merge
RUN chmod +x /app/gtfs-merge

# Download the OneBusAway merge CLI JAR
RUN curl \
    -L https://repo1.maven.org/maven2/org/onebusaway/onebusaway-gtfs-merge-cli/${JAR_VERSION}/onebusaway-gtfs-merge-cli-${JAR_VERSION}.jar \
    -o merge-cli.jar

# Use ENTRYPOINT for the Go binary
ENTRYPOINT ["/app/gtfs-merge"]
CMD []