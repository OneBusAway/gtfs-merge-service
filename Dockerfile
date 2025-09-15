FROM eclipse-temurin:17-jre

ARG JAR_VERSION=9.0.1
ENV JAR_VERSION=${JAR_VERSION}

# Install bash, curl for downloading, and unzip for AWS CLI installation
RUN apt-get update && \
    apt-get install -y \
    bash \
    curl \
    unzip && \
    rm -rf /var/lib/apt/lists/*

# Copy and run AWS CLI installation script
COPY install-awscli.sh /tmp/install-awscli.sh
RUN /tmp/install-awscli.sh && \
    rm /tmp/install-awscli.sh

# Set working directory
WORKDIR /app

RUN curl \
    -L https://repo1.maven.org/maven2/org/onebusaway/onebusaway-gtfs-merge-cli/${JAR_VERSION}/onebusaway-gtfs-merge-cli-${JAR_VERSION}.jar \
    -o merge-cli.jar

CMD ["tail", "-f", "/dev/null"]