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

# Copy and run AWS CLI installation script
COPY install-awscli.sh /tmp/install-awscli.sh
RUN /tmp/install-awscli.sh && \
    rm /tmp/install-awscli.sh

# Set working directory
WORKDIR /app

COPY merge.sh ./merge.sh
RUN chmod +x ./merge.sh

RUN curl \
    -L https://repo1.maven.org/maven2/org/onebusaway/onebusaway-gtfs-merge-cli/${JAR_VERSION}/onebusaway-gtfs-merge-cli-${JAR_VERSION}.jar \
    -o merge-cli.jar

# Use ENTRYPOINT for the main script so it always runs; CMD can be used to pass arguments or be overridden
ENTRYPOINT ["/app/merge.sh"]
CMD []