# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Docker-based service that merges multiple GTFS (General Transit Feed Specification) feeds into a single feed and can upload the result to AWS S3. It uses the OneBusAway GTFS Merge CLI tool internally, wrapped in a Go application for better configuration management and error handling.

## Key Commands

### Build and run locally (for development)
```bash
cd merge
make test-unit        # Run unit tests
make test-integration # Run integration tests (requires JAR)
go run cmd/gtfs-merge/main.go --config ../example-configs/puget-sound.json
```

### Build the Docker image
```bash
docker build --tag gtfs-merge-service .
```

### Run the container
```bash
docker run -e AWS_ACCESS_KEY_ID=xxx -e AWS_SECRET_ACCESS_KEY=yyy \
  -v $(pwd)/config.json:/config.json \
  gtfs-merge-service --config /config.json
```

## Architecture

The service consists of:

1. **Go Application** (`merge/`):
   - `cmd/gtfs-merge/main.go`: Entry point that orchestrates the merge process
   - `internal/config/`: Configuration parsing and validation
   - `internal/download/`: GTFS feed downloading logic
   - `internal/merge/`: OneBusAway JAR execution wrapper
   - `internal/validate/`: GTFS feed validation
   - `internal/upload/`: S3 upload functionality

2. **Dockerfile**: Multi-stage build that:
   - Builds the Go binary in an Alpine container
   - Creates a runtime image with Java 17 JRE (for OneBusAway JAR)
   - Downloads the OneBusAway GTFS Merge CLI JAR from Maven Central
   - Installs AWS CLI v2 for S3 operations (supports both x86_64 and aarch64)

3. **Configuration**: JSON-based configuration that specifies:
   - Input GTFS feed URLs
   - Agency renaming rules
   - Output file location (local or S3)
   - Optional validation settings

## Configuration Format

```json
{
  "feeds": [
    {
      "url": "https://example.com/gtfs.zip",
      "agencyIdMapping": {"old_id": "new_id"}
    }
  ],
  "output": {
    "type": "s3",
    "bucket": "my-bucket",
    "key": "merged.zip"
  },
  "validate": true
}
```

## Testing

```bash
cd merge
make test-unit        # Unit tests only
make test-integration # Integration tests (requires JAR)
```

## Important Notes

- The OneBusAway JAR version is set to 9.0.1 (configurable via `JAR_VERSION` build arg)
- The service validates GTFS feeds before and after merging when configured
- S3 uploads require AWS credentials via environment variables or IAM role
- Output can be uploaded to S3-compatible storage services; requires AWS credentials set via .env
- The Go binary handles all orchestration; the JAR is only used for the actual merge operation