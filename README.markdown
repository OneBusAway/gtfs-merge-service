# OneBusAway GTFS Merge Service

This repository contains the artifacts necessary to build a Docker image from the [onebusaway-gtfs-modules](https://github.com/OneBusAway/onebusaway-gtfs-modules) merge CLI that can be invoked with a collection of static GTFS feeds, instructions for merging them, and a destination S3 bucket for storing the resulting output.

## Why would you want to use this?

This tool can be used to automate the creation of a merged static GTFS bundle whenever an input changes. By including the ability to upload the resulting merged feed to AWS S3 or an S3 compatible service, it's possible to now automate the creation of a new OBA-compatible data bundle.

# Usage

## Build the image

```bash
docker build --tag oba-merge-service .
```

## Set environment variables

The service requires several environment variables to be set:

- `AWS_ACCESS_KEY_ID`: AWS/R2 access key ID
- `AWS_SECRET_ACCESS_KEY`: AWS/R2 secret access key
- `AWS_ENDPOINT_URL`: S3-compatible endpoint URL (or Cloudflare R2)
- `S3_BUCKET`: Destination bucket for the merged GTFS feed
- `ALLOWED_DOMAINS`: Comma-separated list of allowed domains for config and feed URLs (security feature)
- `JAVA_OPTS` (optional): JVM flags passed to the merge CLI. Use this to increase heap size for large feeds (e.g. `-Xmx1G -server`).

Copy env.example to .env and fill in the file.

```
AWS_ACCESS_KEY_ID=your-access-key
AWS_SECRET_ACCESS_KEY=your-secret-key
AWS_ENDPOINT_URL=https://your-account.r2.cloudflarestorage.com
S3_BUCKET=your-bucket-name
ALLOWED_DOMAINS=example.com,transit.agency.gov
JAVA_OPTS=-Xmx1G -server
```

## Run the container

```bash
docker run \
  --env-file .env
  -v ./example-configs:/config \
  oba-merge-service -config-path /config/puget-sound.json
```

### Configuration File Format

The service expects a JSON configuration file with the following structure:

```json
{
  "feeds": [
    "https://example.com/gtfs/feed1.zip",
    "https://example.com/gtfs/feed2.zip"
  ],
  "mergeStrategies": {
    "agency.txt": "identity",
    "stops.txt": "fuzzy",
    "routes.txt": "fuzzy",
    "trips.txt": "identity",
    "stop_times.txt": "identity",
    "calendar.txt": "identity",
    "shapes.txt": "fuzzy",
    "transfers.txt": "none"
  },
  "outputName": "merged-gtfs.zip"
}
```

# Apache 2.0 License

Copyright 2025 Open Transit Software Foundation

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
