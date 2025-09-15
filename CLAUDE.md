# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Docker-based service that wraps the OneBusAway GTFS Merge CLI tool. It merges multiple GTFS (General Transit Feed Specification) feeds into a single feed and can upload the result to AWS S3.

## Key Commands

### Build the Docker image
```bash
docker build --tag oba-merge-service .
```

### Run the container
```bash
docker run oba-merge-service
```

## Architecture

The service consists of:

1. **Dockerfile**: Multi-architecture Docker image based on Eclipse Temurin Java 17 JRE that:
   - Downloads the OneBusAway GTFS Merge CLI JAR from Maven Central (version 9.0.1)
   - Installs AWS CLI v2 for S3 uploads (supports both x86_64 and aarch64 architectures)
   - Sets up the merge.sh script as the entrypoint

2. **merge.sh**: Main execution script that handles the GTFS merge process and coordinates between the Java CLI tool and AWS S3 operations

3. **install-awscli.sh**: Helper script that detects system architecture and installs the appropriate AWS CLI v2 version

## Important Notes

- The JAR version is parameterized in the Dockerfile as `JAR_VERSION` (default: 9.0.1)
- The service expects to work with static GTFS feeds and merge instructions
- Output can be uploaded to S3-compatible storage services
- The merge.sh script is the main entry point and should contain the logic for downloading feeds, merging them, and uploading results