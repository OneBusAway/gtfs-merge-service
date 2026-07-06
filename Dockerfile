# Selects which jars-* provider stage feeds the runtime image (see "Stages
# 2a/2b" below). Declared before the first FROM so the
# `FROM jars-${JAR_PROVIDER}` selector stage can expand it.
ARG JAR_PROVIDER=release

# Stage 1: Build the Go binary
FROM golang:1-alpine AS builder

WORKDIR /build

# Copy go mod files
COPY merge/go.mod merge/go.sum ./
RUN go mod download

# Copy source code
COPY merge/ ./

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o gtfs-merge cmd/gtfs-merge/main.go

# Stages 2a/2b: obtain the OneBusAway CLI JARs.
#
# Two interchangeable provider stages satisfy the same contract — a runnable
# (shaded, Main-Class-bearing) fat JAR at /jars/merge-cli.jar and
# /jars/transformer-cli.jar — and the JAR_PROVIDER build arg picks which one
# feeds the runtime image. The contract is asserted once, in the jars-verified
# stage below, so a provider that ships a thin or wrong jar fails the build
# loudly. BuildKit only builds the selected stage, so the default build never
# clones or compiles gtfs-modules.
#
#   release (default): download the pinned JAR_VERSION release of both CLIs
#     from Maven Central. Fast and dependency-light; prefer this whenever a
#     release carries everything we need.
#   source: build both CLIs from a pinned OneBusAway/onebusaway-gtfs-modules
#     git SHA. Cut over to this when a needed fix or flag has merged upstream
#     but no release ships it yet (as happened with --duplicateRenaming before
#     v14.1.0 — see issue #2):
#       docker build --build-arg JAR_PROVIDER=source \
#                    --build-arg GTFS_MODULES_REF=<sha> .
#
# gtfs-modules v14 compiles with <release>25</release>, so the source builder
# and the runtime image both use Java 25 (the v14 class files don't run on
# older JREs).

# Stage 2a: download the released CLI JARs from Maven Central.
#
# Pinned by version AND sha256 digest (the digests are published beside each
# artifact as <jar>.sha256): a JAR_VERSION bump must update the digests too,
# keeping upgrades deliberate and downloads tamper-evident. busybox wget and
# sha256sum cover everything this stage does, so no packages are installed.
FROM alpine:3 AS jars-release

ARG JAR_VERSION=14.1.0
ARG MERGE_CLI_SHA256=79f8777493aee236e379e6220607bc1fc866fe1d40e677c38aad4ced3cf026bb
ARG TRANSFORMER_CLI_SHA256=364bbd4d3519d491dd406944b29c1bed3a0e00134b9a9ce9c327390fa7209709

RUN set -e; mkdir -p /jars; \
    for triple in "onebusaway-gtfs-merge-cli:merge-cli.jar:${MERGE_CLI_SHA256}" \
                  "onebusaway-gtfs-transformer-cli:transformer-cli.jar:${TRANSFORMER_CLI_SHA256}"; do \
      mod="${triple%%:*}"; rest="${triple#*:}"; out="${rest%%:*}"; sha="${rest#*:}"; \
      wget -q "https://repo1.maven.org/maven2/org/onebusaway/${mod}/${JAR_VERSION}/${mod}-${JAR_VERSION}.jar" \
        -O "/jars/$out"; \
      echo "$sha  /jars/$out" | sha256sum -c -; \
    done

# Stage 2b: build the CLI JARs from gtfs-modules source.
#
# Both CLIs are built from the same tree so the merge- and transformer-cli
# stay on a single, coherent gtfs-modules version. Pinned by full SHA (not
# branch) for reproducible image builds; bump GTFS_MODULES_REF deliberately.
FROM maven:3.9-eclipse-temurin-25 AS jars-source

# onebusaway-gtfs-modules docs-and-cli (PR #471), on top of 14.0.1-SNAPSHOT
ARG GTFS_MODULES_REF=f9cd94d44facfee8234ab6684bd59ce831168193

# The maven-shade-plugin replaces each module's main artifact with a runnable
# fat JAR, leaving the non-runnable thin jar as original-*.jar beside it. Select
# that one shaded JAR per module by excluding the original-*.jar (and, defensively,
# any -sources/-javadoc jars — already suppressed by the skip flags above). The
# two modules name their main artifact differently (one keeps the version, one
# doesn't), so match by exclusion rather than a fixed name, and assert exactly
# one match so a future GTFS_MODULES_REF bump that adds a stray classifier jar
# fails the build loudly instead of silently shipping the wrong jar. (The
# fat-jar/Main-Class assertion lives in the shared jars-verified stage below.)
RUN git clone https://github.com/OneBusAway/onebusaway-gtfs-modules.git /src && \
    cd /src && \
    git checkout ${GTFS_MODULES_REF} && \
    mvn -q -pl onebusaway-gtfs-merge-cli,onebusaway-gtfs-transformer-cli -am package \
        -DskipTests -Dmaven.source.skip=true -Dmaven.javadoc.skip=true && \
    mkdir -p /jars && \
    for pair in "onebusaway-gtfs-merge-cli:merge-cli.jar" \
                "onebusaway-gtfs-transformer-cli:transformer-cli.jar"; do \
      mod="${pair%%:*}"; out="${pair##*:}"; \
      matches="$(find "$mod/target" -maxdepth 1 -name '*.jar' \
                   ! -name '*-sources.jar' ! -name '*-javadoc.jar' ! -name 'original-*.jar')"; \
      count="$(printf '%s' "$matches" | grep -c .)"; \
      [ "$count" -eq 1 ] || { echo "ERROR: expected 1 shaded jar in $mod/target, found $count:" >&2; printf '%s\n' "$matches" >&2; exit 1; }; \
      cp "$matches" "/jars/$out"; \
    done

# Selector: resolves to the provider stage JAR_PROVIDER names.
FROM jars-${JAR_PROVIDER} AS jars

# Shared contract check, run against whichever provider was selected: both
# jars must exist (the COPY fails otherwise) and be runnable fat JARs with a
# Main-Class in the manifest. Providers only need to produce the files; any
# present or future provider gets this assertion for free.
FROM alpine:3 AS jars-verified
COPY --from=jars /jars/merge-cli.jar /jars/transformer-cli.jar /jars/
RUN set -e; for jar in /jars/merge-cli.jar /jars/transformer-cli.jar; do \
      unzip -p "$jar" META-INF/MANIFEST.MF | grep -q '^Main-Class:' || \
        { echo "ERROR: $jar has no Main-Class (not a runnable fat jar)" >&2; exit 1; }; \
    done

# Stage 3: Runtime image
FROM eclipse-temurin:25-jre

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

# Copy the OneBusAway CLI JARs from the selected, verified provider stage.
COPY --from=jars-verified /jars/merge-cli.jar /app/merge-cli.jar
COPY --from=jars-verified /jars/transformer-cli.jar /app/transformer-cli.jar

# Use ENTRYPOINT for the Go binary
ENTRYPOINT ["/app/gtfs-merge"]
CMD []
