#!/usr/bin/env bash

set -e

here's what this script needs to do:
1. Validate that it has the necessary ENV variables for S3 upload access
2. accept the URL to a JSON config file
3. download the config file and validate that it has the necessary fields
4. download each GTFS feed specified in the config file to a temp directory
5. run the OneBusAway GTFS merge CLI to merge the feeds using the processing instructions in the config file
6. validate the merged feed(?)
7. upload the merged feed to the specified S3 bucket

echo "Starting GTFS merge process..."
echo "$@"