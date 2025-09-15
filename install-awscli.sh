#!/bin/bash
set -e

# Detect system architecture
ARCH=$(uname -m)

echo "Detected architecture: $ARCH"

# Set the appropriate AWS CLI download URL based on architecture
if [ "$ARCH" = "x86_64" ]; then
    AWS_CLI_URL="https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip"
elif [ "$ARCH" = "aarch64" ]; then
    AWS_CLI_URL="https://awscli.amazonaws.com/awscli-exe-linux-aarch64.zip"
else
    echo "Unsupported architecture: $ARCH"
    exit 1
fi

echo "Downloading AWS CLI v2 from: $AWS_CLI_URL"

# Download AWS CLI v2
curl -o /tmp/awscliv2.zip "$AWS_CLI_URL"

# Extract the installer
unzip -q /tmp/awscliv2.zip -d /tmp

# Install AWS CLI v2
/tmp/aws/install

# Clean up temporary files
rm -rf /tmp/awscliv2.zip /tmp/aws

# Verify installation
aws --version

echo "AWS CLI v2 installation complete"