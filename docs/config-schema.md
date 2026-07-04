# GTFS Merge Pipeline — Config & Report Schema

This document is the capability contract for the merge pipeline v2 overhaul. It
defines:

1. The v2 job-config schema consumed by the Go service.
2. The transform-rule op vocabulary accepted by the OneBusAway GTFS Transformer
   CLI (`transformer-cli.jar`), pinned against the actual parser source.
3. The `report.json` v1 schema produced by a merge run (implemented in a later
   milestone; documented here so the config and report evolve together).
4. The legacy (v1) config schema, kept for backward compatibility.

Everything in this document reflects the real behavior of
`onebusaway-gtfs-merge-cli` and `onebusaway-gtfs-transformer-cli` version
`11.2.2`, as verified against their source in `gtfs-modules`.

## 1. Config schema v2

### 1.1 Top-level shape

```json
{
  "version": 2,
  "output": {
    "key": "combined-feeds/12/5/builds/49/gtfs.zip",
    "reportKey": "combined-feeds/12/5/builds/49/report.json"
  },
  "feeds": [
    {
      "id": "everett",
      "name": "Everett Transit",
      "url": "https://example.com/everett.zip",
      "prefix": "97-",
      "transformRules": [
        {"op": "update", "match": {"file": "agency.txt", "agency_id": "1"}, "update": {"agency_id": "97"}}
      ],
      "pairedWith": {"url": "https://example.com/everett-upcoming.zip"}
    }
  ],
  "sharedTransformRules": [],
  "mergeSettings": {
    "duplicateHandling": "log",
    "files": {
      "stops.txt": {"detection": "fuzzy", "renaming": "agency"},
      "trips.txt": {"detection": "identity", "renaming": "agency"}
    }
  },
  "additionalFiles": [
    {"filename": "translations.txt", "url": "https://example.com/translations.txt"}
  ]
}
```

### 1.2 Field reference

| Key | Type | Required | Description |
|---|---|---|---|
| `version` | int | yes | Must be `2` for this schema. Any other value (or a missing key) is parsed as the v1 schema — see Appendix. |
| `output.key` | string | yes | Destination object key for the merged `gtfs.zip` in the output bucket. Must not contain `..`. |
| `output.reportKey` | string | yes | Destination object key for `report.json` (see §3). Must not contain `..`. |
| `feeds` | array of Feed | yes, non-empty | Input feeds, **in merge order**. See §1.3 for merge-order semantics. |
| `sharedTransformRules` | array of raw JSON objects | no | Transformer-native rule objects (see §2) applied, in array order, to **every** feed after its own `feeds[].transformRules` and before the big merge. Passed through verbatim, one object per line, to `transformer-cli.jar`. |
| `mergeSettings.duplicateHandling` | enum: `ignore` \| `log` \| `fail` | no (default `ignore`) | **Global** — applies to the whole merge run, not per file. See §1.4. |
| `mergeSettings.files` | map\<filename, FileMergeSetting\> | no | Per-file duplicate detection/renaming overrides for files with an independent merge strategy. See §1.5. |
| `additionalFiles` | array of AdditionalFile | no | Files that are downloaded and copied into the merged output as-is (not merged, not transformed) — e.g. `translations.txt`. |

**Feed object:**

| Key | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes | Unique identifier for this feed within the job. Must match `^[a-z0-9_-]+$`. Used to key report.json sections and (via `prefix`) bucket dropped-duplicate/prefix stats. |
| `name` | string | no | Human-readable label, used only in the report. |
| `url` | string | yes | Source GTFS zip URL. Must pass the service's `ALLOWED_DOMAINS` check (same `validateURL` used for v1 feed URLs). |
| `prefix` | string | no | **Informational only.** Used for report bucketing (e.g. `prefixHistogram`) — it is never passed to the merge or transform JARs. If you want IDs actually prefixed, do it with a `transformRules` `update` op (see §2.4) or via `mergeSettings.files[...].renaming`. |
| `transformRules` | array of raw JSON objects | no | Transformer-native rule objects (§2), passed through verbatim, applied to this feed only, before the big merge. Run in array order, after `pairedWith` pre-merge (if present). |
| `pairedWith.url` | string | no | A second signup zip for the same agency (e.g. an "upcoming" feed). If present, `pairedWith.url` is merged into `url` using **default** merge settings (JAR auto-detection, no explicit `--file`/`--duplicateDetection` flags) as a preparatory step, **before** this feed participates in the main multi-feed merge. |

**AdditionalFile object:**

| Key | Type | Required | Description |
|---|---|---|---|
| `filename` | string | yes | Must match `^[A-Za-z0-9._-]+$` (no path separators). |
| `url` | string | yes | Source URL; same domain allowlist as feed URLs. |

### 1.3 Merge-order semantics

`feeds` is an ordered array, and **order is merge order**. The underlying JAR
(`onebusaway-gtfs-merge-cli`) is invoked with the feeds' downloaded directories
as positional arguments in the same order they appear in `feeds`, followed by
the output path (`GtfsMergerMain`: `List<File> inputPaths = files.subList(0,
files.size() - 1)`).

The JAR's entity merge strategies resolve ID collisions in **reverse list
order: the last feed in the list wins** — it keeps its native (unrenamed) IDs,
and earlier feeds' colliding entities are either dropped (duplicate) or
renamed, depending on the file's duplicate detection/renaming strategy. If
determinism matters (e.g. a specific agency's IDs must survive collisions),
put that feed **last**.

This is unrelated to `prefix`/`pairedWith`, which only affect a single feed's
own preparation, not the cross-feed merge order.

### 1.4 `mergeSettings.duplicateHandling` (global)

| Value | JAR flag | Behavior |
|---|---|---|
| `ignore` (default) | *(neither flag passed)* | Dropped duplicates are silently discarded. |
| `log` | `--logDroppedDuplicates` | Dropped duplicates are logged as warnings. |
| `fail` | `--errorOnDroppedDuplicates` | The merge run fails if any duplicate entity is dropped. |

This is a single, job-wide setting — the JAR flags are global, not
per-file, so there is no `mergeSettings.files[...].duplicateHandling`.

### 1.5 `mergeSettings.files[filename]` (per-file)

| Key | Enum values | Meaning |
|---|---|---|
| `detection` | `identity` \| `fuzzy` \| `none` | Duplicate **detection** strategy for this file (`--duplicateDetection`). |
| `renaming` | `context` \| `agency` | Duplicate **renaming** strategy for this file (`--duplicateRenaming`). `context` prefixes IDs with an index-derived prefix (`a-`, `b-`, ...); `agency` prefixes IDs with the owning feed's agency ID. |

Only files with an **independent** merge strategy in the JAR may be keys of
`mergeSettings.files`:

```
agency.txt, stops.txt, routes.txt, trips.txt, calendar.txt, shapes.txt,
frequencies.txt, transfers.txt, fare_attributes.txt, fare_rules.txt,
feed_info.txt, areas.txt
```

(Verified against `onebusaway-gtfs-merge`'s `strategies/` package — each of
these files has its own `*MergeStrategy` class registered with
`AbstractEntityMergeStrategy`, and each is addressable via its own
`--file`/`--duplicateDetection`/`--duplicateRenaming` triple, since those are
index-paired lists in `GtfsMergerMain`.)

Two files have **no independent strategy** — they always follow their parent
file's strategy inside the JAR:

| File | Follows |
|---|---|
| `stop_times.txt` | `trips.txt` |
| `calendar_dates.txt` | `calendar.txt` |

Passing a follows-file as a `--file` to the JAR silently overwrites its
parent's strategy rather than doing anything useful for the follows-file
itself. **Config v2 validation rejects this outright**: a follows-file key in
`mergeSettings.files` produces the error:

```
"%s has no independent strategy; it follows %s"
```

e.g. `"stop_times.txt has no independent strategy; it follows trips.txt"`.

**Files omitted from `mergeSettings.files` fall back to JAR auto-detection**
(the JAR's own default per-file heuristic, not `identity`/`none`/etc. — it is
whatever the JAR picks when no `--file` entry names that file at all).
Callers who need deterministic, reproducible merges must explicitly list
every independent file they care about; do not rely on the JAR's built-in
default persisting across JAR upgrades.

## 2. Transform-rule op vocabulary

Transform rules (`feeds[].transformRules`, `sharedTransformRules`) are
**transformer-native rule objects**, passed through verbatim — one JSON object
per line — to `transformer-cli.jar` via:

```
java -jar transformer-cli.jar --transform=<path> input.zip output.zip
```

(`GtfsTransformerMain`: `ARG_TRANSFORM = "transform"`, dispatched to
`GtfsTransformerLibrary.configureTransformation`, which ultimately calls
`TransformFactory.addModificationsFromReader` — one JSON object parsed per
non-blank, non-`#`-comment line.)

The Go config layer does **not** validate the contents of a `transformRules`
entry beyond "is this syntactically valid JSON" (it is stored as
`json.RawMessage`). The op vocabulary below is what the JAR itself accepts at
runtime — quoted from `TransformFactory.java`
(`onebusaway-gtfs-transformer/src/main/java/org/onebusaway/gtfs_transformer/factory/TransformFactory.java`,
method `addModificationsFromReader`).

### 2.1 Every op the parser recognizes

Every rule line must have an `"op"` key. `TransformFactory` dispatches on its
exact string value (unrecognized values throw `unknown transform op`):

| `op` value(s) | Handler | Purpose |
|---|---|---|
| `add` | `handleAddOperation` | Construct and inject a brand-new entity. |
| `update`, `change`, `modify` (aliases of the same handler) | `handleUpdateOperation` | Update matching entities' fields (or run a custom factory/string-replacement strategy). |
| `remove`, `delete` (aliases) | `handleRemoveOperation` | Delete matching entities. |
| `retain` | `handleRetainOperation` | Keep only matching entities (and their transitive dependents/dependencies); drop everything else. |
| `subsection` | `handleSubsectionOperation` | Trim trips to a stop subsection (`SubsectionTripTransformStrategy`); needs `from_stop_id`/`to_stop_id` via CSV-style bean fields. |
| `trim_trip` | `handleTrimOperation` | Trim a specific trip's stop-time range between two stop IDs. |
| `stop_times_factory` | `handleStopTimesOperation` | Runs `StopTimesFactoryStrategy` configured from the JSON's bean fields. |
| `calendar_extension` | → `CalendarExtensionStrategy` | |
| `thirty_day_calendar_extension` | → `ThirtyDayCalendarExtensionStrategy` | |
| `calendar_simplification` | → `CalendarSimplicationStrategy` | |
| `deduplicate_service_ids` | → `DeduplicateServiceIdsStrategy` | |
| `shift_negative_stop_times` | → `ShiftNegativeStopTimesUpdateStrategy` | |
| `shape_direction` | → `ShapeDirectionTransformStrategy` | |
| `remove_non_revenue_stops` | → `RemoveNonRevenueStopsStrategy` | |
| `remove_non_revenue_stops_excluding_terminals` | → `RemoveNonRevenueStopsExcludingTerminalsStrategy` | |
| `update_trip_headsign_by_destination` | → `UpdateTripHeadsignByDestinationStrategy` | |
| `update_trip_headsign_exclude_nonreference` | → `UpdateTripHeadsignExcludeNonreference` | |
| `update_trip_headsign_if_null` | → `UpdateTripHeadsignIfNull` | |
| `update_trip_headsign_railroad_convention` | → `UpdateTripHeadsignRailRoadConvention` | |
| `merge_stop_names_from_reference` | → `MergeStopNamesFromReferenceStrategy` | |
| `merge_stop_ids_from_reference` | → `MergeStopIdsFromReferenceStrategy` | |
| `update_stop_ids_from_control` | → `UpdateStopIdFromControlStrategy` | |
| `update_wrong_way_concurrencies` | → `UpdateWrongWayConcurrencies` | |
| `update_stop_ids_from_file` | → `UpdateStopIdsFromFile` | |
| `update_stop_ids_from_reference` | → `UpdateStopIdFromReferenceStrategy` | |
| `merge_route_from_reference_by_longname` | → `MergeRouteFromReferenceStrategyByLongName` | |
| `merge_route_from_reference_by_id` | → `MergeRouteFromReferenceStrategyById` | |
| `merge_route_from_reference` | → `MergeRouteFromReferenceStrategy` | |
| `merge_route_five` | → `MergeRouteFive` | |
| `update_route_name` | → `UpdateRouteNames` | |
| `validate_gtfs` | → `ValidateGTFS` | |
| `count_and_test` | → `CountAndTest` | |
| `count_and_test_bus` | → `CountAndTestBus` | |
| `count_and_test_subway` | → `CountAndTestSubway` | |
| `verify_bus_service` | → `VerifyBusService` | |
| `update_stoptimes_for_time` | → `UpdateStopTimesForTime` | |
| `last_stop_to_headsign` | → `LastStopToHeadsignStrategy` | |
| `remove_current_service` | → `RemoveCurrentService` | |
| `check_for_future_service` | → `CheckForFutureService` | |
| `remove_unused_routes` | → `RemoveUnusedRoutes` | |
| `remove_old_calendar_statements` | → `RemoveOldCalendarStatements` | |
| `truncate_calendar_statements` | → `TruncateNewCalendarStatements` | |
| `check_for_plausible_stop_times`, `check_for_stop_times_without_stops` (both map to the same class) | → `CheckForPlausibleStopTimes` | |
| `check_for_lengthy_route_names` | → `CheckForLengthyRouteNames` | |
| `ensure_direction_id_exists` | → `EnsureDirectionIdExists` | |
| `ensure_route_long_name_exists` | → `EnsureRouteLongNameExists` | |
| `anomaly_check_future_trip_counts` | → `AnomalyCheckFutureTripCounts` | |
| `verify_future_route_service` | → `VerifyFutureRouteService` | |
| `verify_reference_service` | → `VerifyReferenceService` | |
| `sanitize_for_api_access` | → `SanitizeForApiAccess` | |
| `add_omny_lirr_data` | → `AddOmnyLIRRData` | |
| `add_omny_bus_data` | → `VerifyRouteIds` *(sic — the JAR wires this op name to `VerifyRouteIds`, not an OMNY-bus-specific class; verified in source, not a typo in this doc)* | |
| `KCMSuite` | *(hardcoded King County Metro preset — adds several fixed strategies and pulls remote modification files)* | Not recommended for general use; agency-specific legacy preset. |
| `transform` | `handleTransformOperation(line, json)` | Generic escape hatch: instantiate an arbitrary `class` (fully-qualified Java class name) as a `GtfsTransformStrategy`/`GtfsEntityTransformStrategy`/`GtfsTransformStrategyFactory` and populate its bean fields from the remaining JSON keys. |

Rows without a dedicated line above (`calendar_extension` through
`add_omny_bus_data`, plus `transform`) all go through
`handleTransformOperation`, which instantiates the named strategy class and
sets its bean properties directly from the JSON object's other keys (via
`setObjectPropertiesFromJsonUsingCsvFields`) — i.e. the JSON shape for those
ops is "whatever public bean setters that strategy class has," not a fixed
`match`/`update` shape.

Lines that are empty, start with `#`, or are exactly `{{{`/`}}}` are skipped
(comment/fold markers).

### 2.2 Common JSON shape (`add`, `update`/`change`/`modify`, `remove`/`delete`, `retain`, `trim_trip`)

These five ops share a `match`-based addressing scheme:

- **`match`** (object): identifies which entities to operate on.
  - **`file`** (string, e.g. `"agency.txt"`) — the *only* thing this doc
    recommends config authors use to name the entity type. Internally it
    resolves to the CSV schema's entity class
    (`getEntityClassFromJsonSpec`); `class` (fully-qualified Java class name)
    is also accepted as an alternative to `file` for esoteric cases.
  - All other keys in `match` are **property matchers**: `"agency_id": "1"`
    means "the entity's `agency_id` property equals `1`". A property name
    wrapped as `"any(...)"` matches against a collection property. Values
    can be an exact match, or (per `DeferredValueMatcher`) a regex/typed
    match depending on the field's schema type.
  - A `match` with **only** `file` (no other property keys) matches **every**
    entity of that type (`EntityMatchCollection` over zero matchers is
    vacuously true for every object) — useful for "update the one
    `feed_info.txt` row" or "remove all `frequencies.txt` rows" patterns.
  - `match.collection` is a special case for two synthetic types:
    `{"collection": "calendar", "service_id": "..."}` and
    `{"collection": "shape", "shape_id": "..."}` (whole-calendar /
    whole-shape operations).
- **`obj`** (object, `add` only): same `file`/`class` addressing as `match`,
  plus the property values to set on the newly created entity.
- **`update`** (object, `update`/`change`/`modify` only): property values to
  set on each matched entity. A value can also be `"path(...)"` (resolve via
  a property-path expression against another entity) or `"s/from/to/"`
  (regex replace against the existing value).
- **`retainUp`** (bool, `retain` only, default `true`) / **`retainBlocks`**
  (bool, `retain` only): control which direction the retention graph walks
  and whether whole trip blocks are retained together.
- **`to_stop_id`** / **`from_stop_id`** (strings, `trim_trip` only): at least
  one is required. `trim_trip`'s `match` must resolve to `Trip` (i.e.
  `match.file` must be `trips.txt`, or `match.class` must be
  `org.onebusaway.gtfs.model.Trip`) — enforced explicitly (`the trim_trip op
  only supports matching against trips`).

### 2.3 Worked example — stamp a `feed_id`

Add a `feed_info.txt` row (works even if the source feed has none) with a
unique `feed_id` so downstream reporting/sampling can attribute rows back to
this feed:

```json
{"op": "add", "obj": {"file": "feed_info.txt", "feed_id": "everett", "feed_publisher_name": "Everett Transit", "feed_publisher_url": "https://everetttransit.org", "feed_lang": "en"}}
```

### 2.4 Worked example — remap `agency_id`

Rewrite `agency.txt`'s row with `agency_id` `"1"` to `agency_id` `"97"` (and,
implicitly, every other entity's `agency_id` foreign key follows via the
transformer's reference-fixup machinery):

```json
{"op": "update", "match": {"file": "agency.txt", "agency_id": "1"}, "update": {"agency_id": "97"}}
```

This is exactly the `transformRules` entry shown in the schema example in
§1.1.

### 2.5 Worked example — retain a single route

Drop everything from the feed except route `100` and whatever it transitively
depends on (its trips, stop times, stops, shapes, calendar entries, etc.):

```json
{"op": "retain", "match": {"file": "routes.txt", "route_id": "100"}}
```

Add `"retainBlocks": true` if trips sharing a block ID with a retained trip
should also survive.

### 2.6 Worked example — trim a trip between two stops

Cut trip `12345678`'s stop-time sequence down to the portion between stop
`1000` and stop `2000`, inclusive:

```json
{"op": "trim_trip", "match": {"file": "trips.txt", "trip_id": "12345678"}, "from_stop_id": "1000", "to_stop_id": "2000"}
```

## 3. `report.json` v1 schema

`report.json` is written alongside the merged `gtfs.zip` at
`output.reportKey`. It is **specified here in M0 and implemented in a later
milestone (G5)** — the shape below is the contract the rest of the pipeline
should assume is stable.

```json
{
  "reportVersion": 1,
  "generatedAt": "2026-07-04T18:00:00Z",
  "inputs": [
    {
      "feedId": "everett",
      "url": "https://example.com/everett.zip",
      "paired": false,
      "files": ["agency.txt", "stops.txt", "routes.txt", "..."],
      "agencies": [{"agencyId": "97", "name": "Everett Transit"}],
      "counts": {"stops": 1200, "routes": 40, "trips": 8000, "calendars": 6},
      "serviceRange": {"start": "20260101", "end": "20261231"},
      "bbox": {"minLat": 47.8, "maxLat": 48.1, "minLon": -122.4, "maxLon": -122.1},
      "sampleIds": {"stop_id": ["1001", "1002"], "route_id": ["100"], "trip_id": ["12345678"]}
    }
  ],
  "output": {
    "files": ["agency.txt", "stops.txt", "..."],
    "byteSize": 10485760,
    "agencies": [{"agencyId": "97", "name": "Everett Transit"}],
    "countsByAgency": {"97": {"stops": 1200, "routes": 40, "trips": 8000, "calendars": 6}},
    "prefixHistogram": [{"prefix": "97-", "feedId": "everett", "count": 1200}, {"prefix": null, "feedId": "sound-transit", "count": 300}],
    "bbox": {"minLat": 47.0, "maxLat": 48.5, "minLon": -122.8, "maxLon": -121.9},
    "sampleIdMappings": [{"feedId": "everett", "type": "stop_id", "before": "1001", "after": "97-1001"}]
  },
  "merge": {
    "droppedDuplicates": [{"file": "stops.txt", "raw": "duplicate stop 1234 dropped in favor of 5678", "parsed": {"file": "stops.txt", "id": "1234", "keptId": "5678"}}],
    "droppedDuplicatesTruncated": false,
    "renameCounts": {"stops.txt": 42, "routes.txt": 3}
  },
  "stages": [
    {"key": "watch", "feedId": "everett", "status": "ok", "durationMs": 1200},
    {"key": "pair", "feedId": "everett", "status": "ok", "durationMs": 3400},
    {"key": "prepare", "feedId": "everett", "status": "ok", "durationMs": 890},
    {"key": "combine", "status": "ok", "durationMs": 15230},
    {"key": "post", "status": "ok", "durationMs": 640},
    {"key": "report", "status": "ok", "durationMs": 120}
  ],
  "warnings": ["feed 'sound-transit' has no feed_info.txt; feed_id sampling skipped"]
}
```

### 3.1 Field reference

- **`reportVersion`** (int): schema version of this document, `1`.
- **`generatedAt`** (string, RFC 3339 UTC timestamp): when the report was written.
- **`inputs[]`**: one entry per feed in `feeds` (in the same order):
  - `feedId` — matches `feeds[].id`.
  - `url` — the feed's source URL (the primary `url`, not `pairedWith.url`).
  - `paired` (bool) — whether this feed had a `pairedWith` pre-merge step.
  - `files[]` — GTFS filenames present in this input.
  - `agencies[]` — `{agencyId, name}` pairs found in this input's `agency.txt`.
  - `counts` — `{stops, routes, trips, calendars}` row counts.
  - `serviceRange` — `{start, end}` as `YYYYMMDD` strings, the min/max service dates across `calendar.txt`/`calendar_dates.txt`.
  - `bbox` — `{minLat, maxLat, minLon, maxLon}` over this input's `stops.txt`.
  - `sampleIds` — small representative samples of `stop_id`/`route_id`/`trip_id` values, for spot-checking.
- **`output`**: the same shape applied to the merged feed, plus:
  - `byteSize` — size in bytes of the merged `gtfs.zip`.
  - `countsByAgency` — map from `agencyId` to `{stops, routes, trips, calendars}`.
  - `prefixHistogram[]` — `{prefix, feedId, count}`; `prefix` is `null` when the feed declared no `prefix`. Built from the *informational* `feeds[].prefix` field (§1.2), not from actual ID rewriting.
  - `sampleIdMappings[]` — `{feedId, type, before, after}`, showing how a handful of sample IDs from `inputs[].sampleIds` were renamed/rewritten by the merge (renaming strategy) or by transform rules.
- **`merge`**:
  - `droppedDuplicates[]` — up to 500 entries, `{file, raw, parsed?}`; `raw` is the JAR's dropped-duplicate log line, `parsed` is a best-effort structured extraction (may be absent if the line couldn't be parsed).
  - `droppedDuplicatesTruncated` (bool) — `true` if more than 500 duplicates were dropped and the list above was capped.
  - `renameCounts` — map from filename to number of entities renamed by the duplicate-renaming strategy.
- **`stages[]`**: pipeline stage timing/status, in execution order. `key` is one of `watch | pair | prepare | combine | post | report`; `feedId` is present for per-feed stages (`pair`, `prepare`) and absent for whole-job stages (`combine`, `post`, `report`). `status` is `ok` or `error`. Note: `combine` brackets the merge JAR invocation itself, and (per the pipeline diagram from PR #861) the merge+transform+publish work for a whole job is treated as a single logical Render job even though it spans multiple `stages[]` entries here.
- **`warnings[]`**: free-text, non-fatal issues surfaced during report generation (e.g. missing `feed_info.txt`).

## Appendix: config schema v1 (legacy, backward-compatible)

Any config JSON without `"version": 2` is parsed as v1 — this is the format
the service has always accepted, and it will continue to work unchanged.

```json
{
  "feeds": [
    "https://example.com/feed1.zip",
    "https://example.com/feed2.zip"
  ],
  "mergeStrategies": {
    "agency.txt": "identity",
    "stops.txt": "fuzzy"
  },
  "outputName": "merged-gtfs.zip"
}
```

| Key | Type | Required | Description |
|---|---|---|---|
| `feeds` | array of string | yes, non-empty | Feed URLs, in merge order (same last-wins-collisions semantics as v2's `feeds[].url`, per §1.3). Each must pass the `ALLOWED_DOMAINS` check. |
| `mergeStrategies` | map\<filename, strategy\> | no | Per-file **duplicate detection** strategy only (`identity`\|`fuzzy`\|`none`) — there is no v1 equivalent of `duplicateRenaming`, `duplicateHandling`, transform rules, paired feeds, or additional files. |
| `outputName` | string | no (default `"merged-gtfs.zip"`) | Local output filename; uploaded to the configured S3/R2 bucket under this key. |

v1 has no schema-level rejection of follows-files (`stop_times.txt`,
`calendar_dates.txt`) in `mergeStrategies` — the existing `puget-sound.json`
example config sets `stop_times.txt` explicitly today, and the JAR silently
lets it overwrite `trips.txt`'s strategy, same as it always has. This
behavior is preserved as-is for v1; the stricter rejection described in §1.5
applies only to v2's `mergeSettings.files`.
