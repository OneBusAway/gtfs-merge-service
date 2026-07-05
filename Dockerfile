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

# Stage 2: Build the OneBusAway CLI JARs from source.
#
# No released onebusaway-gtfs-merge-cli on Maven Central carries the
# --duplicateRenaming flag, so agency-prefix duplicate renaming
# (mergeSettings.files.<file>.renaming: "agency" in config v2) is unusable from
# a released JAR. That flag exists only on the gtfs-modules PR #471 branch, so
# we build both CLIs from a pinned SHA of that branch until an upstream release
# ships. Both are built from the same tree so the merge- and transformer-cli
# stay on a single, coherent gtfs-modules version. Pinned by full SHA (not
# branch) for reproducible image builds; bump GTFS_MODULES_REF deliberately.
#
# Revert: once a gtfs-modules release containing PR #471 is cut, drop this
# stage and return to release pinning via a JAR_VERSION arg + Maven Central
# curls (see issue #2).
#
# gtfs-modules 14.0.1-SNAPSHOT compiles with <release>25</release>, so this
# stage and the runtime image below both need a Java 25 JDK/JRE (the older
# 11.2.2 JARs ran on 17/21; the v14 class files do not).
FROM maven:3.9-eclipse-temurin-25 AS jars

# onebusaway-gtfs-modules docs-and-cli (PR #471), on top of 14.0.1-SNAPSHOT
ARG GTFS_MODULES_REF=f9cd94d44facfee8234ab6684bd59ce831168193

# The maven-shade-plugin replaces each module's main artifact with a runnable
# fat JAR, leaving the non-runnable thin jar as original-*.jar beside it. Select
# that one shaded JAR per module by excluding the original-*.jar (and, defensively,
# any -sources/-javadoc jars — already suppressed by the skip flags above). The
# two modules name their main artifact differently (one keeps the version, one
# doesn't), so match by exclusion rather than a fixed name. Then assert exactly
# one match and that it is actually a fat jar (has a Main-Class), so a future
# GTFS_MODULES_REF bump that adds a stray classifier jar or drops the shade step
# fails the build loudly instead of silently shipping the wrong/thin jar.
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
      d="$(mktemp -d)"; \
      ( cd "$d" && jar xf "/src/$matches" META-INF/MANIFEST.MF ) && \
        grep -q '^Main-Class:' "$d/META-INF/MANIFEST.MF" || \
        { echo "ERROR: $matches has no Main-Class (shade step likely didn't run)" >&2; exit 1; }; \
      cp "$matches" "/jars/$out"; \
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

# Copy the OneBusAway CLI JARs built (and selected) from source in the jars
# stage.
COPY --from=jars /jars/merge-cli.jar /app/merge-cli.jar
COPY --from=jars /jars/transformer-cli.jar /app/transformer-cli.jar

# Use ENTRYPOINT for the Go binary
ENTRYPOINT ["/app/gtfs-merge"]
CMD []