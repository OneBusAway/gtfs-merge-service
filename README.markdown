# OneBusAway GTFS Merge Service

This repository contains the artifacts necessary to build a Docker image from the [onebusaway-gtfs-modules](https://github.com/OneBusAway/onebusaway-gtfs-modules) merge CLI that can be invoked with a collection of static GTFS feeds, instructions for merging them, and a destination S3 bucket for storing the resulting output.

## Why would you want to use this?

This tool can be used to automate the creation of a merged static GTFS bundle whenever an input changes. By including the ability to upload the resulting merged feed to AWS S3 or an S3 compatible service, it's possible to now automate the creation of a new OBA-compatible data bundle.

# Usage

## Build the image

```bash
docker build --tag oba-merge-service .
```

## Run the container

```bash
docker run oba-merge-service
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
