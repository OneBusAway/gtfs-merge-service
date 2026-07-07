# GTFS Merge Pipeline — Config & Report Schema

This document is the capability contract for the merge pipeline v2 overhaul. It
defines:

1. The v2 job-config schema consumed by the Go service.
2. The transform-rule op vocabulary accepted by the OneBusAway GTFS Transformer
   CLI (`transformer-cli.jar`), pinned against the actual parser source.
3. The `report.json` v1 schema produced by a merge run (implemented in a later
   milestone; documented here so the config and report evolve together).
4. The optional bundle-inputs artifact set (`output.feedKeys` /
   `output.bundleInputsKey`) that OBA servers ingest under multi-zip load.
5. The legacy (v1) config schema, kept for backward compatibility.

Everything in this document reflects the real behavior of
`onebusaway-gtfs-merge-cli` and `onebusaway-gtfs-transformer-cli` as verified
against their source in `gtfs-modules`. The op vocabulary and merge semantics
were pinned against `11.2.2`; the deployed image now ships a v14 JAR
(`14.1.0` was the first release to carry `--duplicateRenaming`, from PR
#471) — see the "JAR provenance" note in §3 — and the documented flags and
log formats were re-verified against the v14 tree.

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
| `output.feedKeys` | map\<feed_id, string\> | no — see §4 | Object key for each feed's prepared zip. |
| `output.bundleInputsKey` | string | no — see §4 | Object key for the bundle-inputs.json manifest. |
| `feeds` | array of Feed | yes, non-empty | Input feeds, **in merge order**. See §1.3 for merge-order semantics. |
| `sharedTransformRules` | array of raw JSON objects | no | Transformer-native rule objects (see §2) applied, in array order, to **every** feed **before** its own `feeds[].transformRules`, and before the big merge. Passed through verbatim, one object per line, to `transformer-cli.jar`. Shared rules run first because they establish the common baseline every feed should get (e.g. dropping unused routes); each feed's own rules then run afterward to refine or override that baseline for its own specifics. |
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
| `transformRules` | array of raw JSON objects | no | Transformer-native rule objects (§2), passed through verbatim, applied to this feed only, before the big merge. Run in array order, after `sharedTransformRules` (if any) and after `pairedWith` pre-merge (if present). |
| `pairedWith.url` | string | no | A second signup zip for the same agency (e.g. an "upcoming" feed). If present, `pairedWith.url` is merged into `url` using **default** merge settings (JAR auto-detection, no explicit `--file`/`--duplicateDetection` flags) as a preparatory step, **before** this feed participates in the main multi-feed merge. |
| `defaultAgencyId` | string | no — see §4 | OBA agency namespace for stops when bundle inputs are enabled. |

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
| `detection` | `identity` \| `fuzzy` \| `none` | Duplicate **detection** strategy for this file (`--duplicateDetection`). Required for every file listed in `mergeSettings.files`. |
| `renaming` | *(empty, default)* \| `context` \| `agency` | Duplicate **renaming** strategy for this file (`--duplicateRenaming`). **Optional** — leave it empty (or omit the key) to fall back to the JAR's own default, which is equivalent to `context`. `context` prefixes IDs with an index-derived prefix (`a-`, `b-`, ...); `agency` prefixes IDs with the owning feed's agency ID. |

**Renaming emission note:** `--file`, `--duplicateDetection`, and
`--duplicateRenaming` are positional, index-paired lists in the JAR
(`GtfsMergerMain.buildMerger` walks `fileOptions` by index and reads
`duplicateRenamingOptions.get(i)` for the same index `i`) — they are **not**
matched up by filename. That means the Go service cannot omit
`--duplicateRenaming` for only *some* of the files in `mergeSettings.files`
without shifting every later file's renaming strategy onto the wrong file.
So the service emits the flag all-or-nothing across the whole
`mergeSettings.files` map for one merge run:

- If **no** file requests `renaming: "agency"`, `--duplicateRenaming` is
  omitted entirely, for every file (this also keeps such configs compatible
  with JAR builds that don't have the flag at all — see the deploy note in
  §3).
- If **any** file requests `renaming: "agency"`, `--duplicateRenaming` is
  emitted for **every** file in the map, using an explicit `context` for
  files that left `renaming` empty or set it to `context` — so the
  positional pairing always stays intact.

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
`output.reportKey`, generated by `internal/report` (`report.Generate`) and
uploaded by `cmd/gtfs-merge`'s `runV2` after the bundle itself uploads
successfully. **Report generation failure is a warning, not a fatal error**:
if it fails (including a panic), the run still exits 0 — the merge already
succeeded by that point — and the failure is logged prominently instead.

> **JAR provenance (agency renaming):**
> `mergeSettings.files[...].renaming: "agency"` (§1.5) depends on a
> `--duplicateRenaming` CLI flag that first shipped in the
> `onebusaway-gtfs-merge-cli` **`14.1.0`** release (added to `gtfs-modules`
> as commit `f9cd94d4` specifically to support this pipeline; no earlier
> release — `11.2.2` through `14.0.0` — has it, see issue #2).
>
> Which JAR the image ships is pinned by the `Dockerfile`'s `JAR_PROVIDER`/
> `JAR_VERSION` build args (a Maven Central release by default, or a
> from-source `gtfs-modules` build pinned by `GTFS_MODULES_REF` instead of
> `JAR_VERSION`, for changes no release ships yet — as was
> the case for this flag before `14.1.0`); see the Dockerfile's "Stages
> 2a/2b" comment for the mechanics. The v14 JARs target Java 25 either way,
> so the runtime image is a Java 25 JRE.
>
> The Go service still only emits `--duplicateRenaming` when at least one
> file in `mergeSettings.files` requests `renaming: "agency"` (see §1.5's
> emission note — the flag can't be omitted for only *some* files without
> misaligning the JAR's positional `--file`/`--duplicateDetection`/
> `--duplicateRenaming` pairing), so detection-only configs and
> `renaming: "context"`/empty configs stay compatible with any JAR that
> lacks the flag, should the image ever pin a pre-`14.1.0` release.

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
    "agencies": [{"agencyId": "97", "name": "Everett Transit"}, {"agencyId": "40", "name": "Sound Transit"}],
    "counts": {"stops": 1500, "routes": 55, "trips": 9500, "calendars": 7},
    "serviceRange": {"start": "20260101", "end": "20261231"},
    "bbox": {"minLat": 47.0, "maxLat": 48.5, "minLon": -122.8, "maxLon": -121.9},
    "sampleIds": {"stop_id": ["1001", "97-1001"], "route_id": ["100"], "trip_id": ["12345678"]},
    "byteSize": 10485760,
    "prefixHistogram": [{"prefix": "97-", "feedId": "everett", "count": 1200}, {"prefix": null, "feedId": "sound-transit", "count": 300}],
    "sampleIdMappings": [{"feedId": "everett", "type": "stop_id", "before": "1001", "after": "97-1001"}]
  },
  "merge": {
    "droppedDuplicates": [{"file": "stops.txt", "raw": "13:00:00.000 [main] WARN AbstractSingleEntityMergeStrategy - duplicate entity: type=class org.onebusaway.gtfs.model.Stop id=97_1234", "parsed": {"id": "97_1234"}}],
    "droppedDuplicatesTruncated": false,
    "renameCounts": {"stops.txt": 42, "trips.txt": 3}
  },
  "stages": [
    {"key": "watch", "status": "ok", "durationMs": 1200},
    {"key": "pair", "feedId": "everett", "status": "ok", "durationMs": 3400},
    {"key": "prepare", "feedId": "everett", "status": "ok", "durationMs": 890},
    {"key": "combine", "status": "ok", "durationMs": 15230},
    {"key": "post", "status": "ok", "durationMs": 640},
    {"key": "report", "status": "ok", "durationMs": 120}
  ],
  "warnings": ["input \"sound-transit\": stops.txt has no stops with valid coordinates"]
}
```

### 3.1 Field reference

- **`reportVersion`** (int): schema version of this document, `1`.
- **`generatedAt`** (string, RFC 3339 UTC timestamp): when the report was written.
- **`inputs[]`**: one entry per feed in `feeds` (in the same order), analyzed against that feed's **final pre-combine working zip** (post pair-merge, post transform — i.e. what actually entered the merge):
  - `feedId` — matches `feeds[].id`.
  - `url` — the feed's source URL (the primary `url`, not `pairedWith.url`).
  - `paired` (bool) — whether this feed had a `pairedWith` pre-merge step.
  - `files[]` — GTFS filenames present in this input.
  - `agencies[]` — `{agencyId, name}` pairs found in this input's `agency.txt`. `agencyId` defaults to `agencyName` (or `""`) when the `agency_id` column is entirely absent (it's optional for single-agency feeds) — but is left as whatever's in a present-but-blank cell, since that's a data quality issue in the source feed, not a missing-column case.
  - `counts` — `{stops, routes, trips, calendars}` row counts. `calendars` is `calendar.txt` row count plus any `calendar_dates.txt` `service_id`s not already in `calendar.txt`.
  - `serviceRange` — `{start, end}` as `YYYYMMDD` strings: min `start_date`/max `end_date` across `calendar.txt`, falling back to min/max `date` across `calendar_dates.txt` only if `calendar.txt` produced no range at all (missing file, or no valid rows) — it is not a union of both.
  - `bbox` — `{minLat, maxLat, minLon, maxLon}` over this input's `stops.txt`; rows with blank/unparseable coordinates are skipped, and so are rows with `location_type >= 3` (generic node, boarding area, and any future values) when a `location_type` column is present. Omitted entirely (not `null`, just absent from the JSON) if no stop had valid coordinates.
  - `sampleIds` — up to 3 `stop_id`/`route_id`/`trip_id` values each, in file (row) order.
- **`output`**: the same shape as one `inputs[]` entry, computed against the final (post-inject) merged zip, plus:
  - `byteSize` — size in bytes of the merged `gtfs.zip`.
  - `prefixHistogram[]` — `{prefix, feedId, count}`, over `stop_id` + `route_id` + `trip_id` combined. This is a **heuristic bucketing based on each feed's configured, informational `prefix` field (§1.2), not on actual ID rewriting**: every feed except the last (highest-priority) one that declared a `prefix` gets a bucket counting output IDs starting with it; the last feed *always* gets a `prefix: null` bucket counting everything not claimed by another bucket, regardless of whether it also declared its own `prefix` — its own IDs are never actually renamed (§1.3), so bucketing it by a declared-but-unused prefix would misrepresent the output. A middle feed that declared no `prefix` gets no bucket at all; its IDs (renamed or not) fall into whichever bucket claims them, most often the last feed's catch-all.
  - `sampleIdMappings[]` — `{feedId, type, before, after}`: for each `inputs[].sampleIds` entry, `after` is `before` unchanged if that exact id exists in the output (a native/unrenamed survivor); else the feed's own configured `prefix` + `before` if *that* form exists in the output; else an index-derived letter prefix (`a-` for the first feed, `b-` for the second, ...) + `before` if *that* form exists (this mirrors the merge JAR's own "context" duplicate-renaming convention, §1.5); a sample matching none of those forms is omitted from `sampleIdMappings` entirely rather than guessed at. `type` is one of `stop_id | route_id | trip_id`.
    - **Known limitation, confirmed against a real merge run:** because the merge JAR only ever renames an entity in response to a genuine raw-id collision, and whichever feed's entity is processed first for a given id always keeps it unrenamed (§1.3's "last feed wins"), a same-named collision *always* leaves a native/unrenamed survivor for that exact id somewhere in the output. Since the native-match check runs first, a losing (renamed) feed's own sample will typically still report `after == before` — matching the *other*, winning feed's surviving entity under the same id text — rather than the letter/prefix form its own entity actually became. In practice, the prefix/letter-index branches only fire for ids a `transformRules` rule rewrote *before* the merge ran, not for ids the merge's own duplicate-renaming strategy rewrote.
- **`merge`**:
  - `droppedDuplicates[]` — up to 500 entries parsed from the merge JAR's captured stdout/stderr, `{file, raw, parsed?}`. Only emitted when `mergeSettings.duplicateHandling` is `log`. `raw` is the JAR's full log line (verified format, `AbstractSingleEntityMergeStrategy.logDuplicateEntity`): `` duplicate entity: type=<class .Class#toString()> id=<AgencyAndId#toString()> ``, e.g. `type=class org.onebusaway.gtfs.model.Stop id=97_1234`. `file` is the GTFS filename the Java entity class maps to. `parsed.id` is the dropped entity's own raw id string (e.g. `97_1234`) — the JAR's log line never names the id of the entity it was kept in favor of, so there is no `keptId`; `parsed` is omitted (not present, rather than `null`) if the line matched `duplicate entity:` but couldn't otherwise be decomposed. A parallel `duplicate key: type=... key=...` message exists for the collection-based merge strategies (`calendar.txt`/`calendar_dates.txt`, `shapes.txt`) but is a distinct format and is not parsed into `droppedDuplicates`.
  - `droppedDuplicatesTruncated` (bool) — `true` if more than 500 duplicates were dropped and the list above was capped.
  - `renameCounts` — map from filename to a **derived, best-effort count** of output ids that appear to have been renamed, for each file present in `mergeSettings.files`. This is derived rather than parsed from a log line because the JAR only logs renames at `DEBUG` (`AbstractIdentifiableSingleEntityMergeStrategy.rename`'s `_log.debug(...)` calls), which this service's default logging verbosity doesn't capture. The derivation counts output ids matching the configured renaming convention's prefix (an index-derived letter for `context`, the owning (non-last) feed's own `agency_id` for `agency`) for each of the seven single-identifiable-id GTFS files (`agency.txt`, `stops.txt`, `routes.txt`, `trips.txt`, `fare_attributes.txt`, `feed_info.txt`, `areas.txt`); `calendar.txt`/`shapes.txt` (a different, key-based collection merge strategy) and `frequencies.txt`/`transfers.txt`/`fare_rules.txt` (no single string id to prefix) are skipped with a warning rather than guessed at.
- **`stages[]`**: pipeline stage timing/status, in execution order. `key` is one of `watch | pair | prepare | combine | post | bundleInputs | report`; `feedId` is present for per-feed stages (`pair`, `prepare`) and absent for whole-job stages (`combine`, `post`, `bundleInputs`, `report`). `status` is `ok` or `error`. Note: `combine` brackets the merge JAR invocation itself, and (per the pipeline diagram from PR #861) the merge+transform+publish work for a whole job is treated as a single logical Render job even though it spans multiple `stages[]` entries here. `bundleInputs` only appears when `output.bundleInputsKey`/`output.feedKeys` are set (see §4.3); it runs after `post` (which brackets the merged-zip upload) and before `report`.
- **`warnings[]`**: free-text, non-fatal issues surfaced during report generation (e.g. a missing expected column, or an input zip that couldn't be analyzed at all).

## 4. Bundle inputs (optional)

When `output.bundleInputsKey` and `output.feedKeys` are both set, the service
additionally uploads each feed's **prepared** working zip (post pair-merge,
post transform — the exact files the combine stage consumed) and a
`bundle-inputs.json` manifest. These are the artifacts an OBA server ingests
under multi-zip load (one `defaultAgencyId` per zip); `output.key`'s merged
zip remains the third-party/download artifact.

### 4.1 Config fields

- `output.feedKeys` — object mapping each feed `id` to the object key its
  prepared zip uploads to. Must cover exactly the configured feeds; keys must
  be non-empty, must not contain `..`, and must be distinct from each other
  and from `output.key` / `output.reportKey` / `output.bundleInputsKey`.
- `output.bundleInputsKey` — object key for the manifest. Required whenever
  `feedKeys` is present (and vice versa); when both are absent the stage is
  skipped entirely (existing configs are unaffected).
- `feeds[].defaultAgencyId` — the OBA agency namespace this feed's stops load
  under (e.g. King County Metro = `"1"`). Required for every feed when bundle
  inputs are enabled; ignored otherwise.

### 4.2 bundle-inputs.json (v1)

```json
{
  "version": 1,
  "feeds": [
    {
      "id": "metro",
      "name": "King County Metro",
      "defaultAgencyId": "1",
      "key": "combined-feeds/12/5/builds/49/feeds/metro.zip",
      "url": "https://<endpoint>/<bucket>/combined-feeds/12/5/builds/49/feeds/metro.zip",
      "byteSize": 1234567,
      "sha256": "…hex…"
    }
  ]
}
```

- Feed order preserves the config's `feeds` order (= merge/roster order =
  intended OBA bundle load order).
- `url` is the per-build, path-style URL (endpoint/bucket/key) — for dry-run
  inspection. At publish, the consuming application (OBACloud) rewrites feed
  URLs to stable keys and injects a top-level `stopConsolidationUrl` **only
  when a stop-consolidation mapping publication exists**. This service never
  emits `stopConsolidationUrl`.
- `byteSize`/`sha256` describe the uploaded zip so the downstream bundler can
  verify its downloads.

### 4.3 Pipeline semantics

- Stage key: `bundleInputs` (report.json `stages[]`; whole-job, no `feedId`).
- Runs after the merged zip uploads and before report generation.
- The feed zips upload first, the manifest last — a manifest never references
  an object that failed to upload.
- **A bundle-inputs upload failure fails the run** (unlike report.json, which
  degrades to a warning): a published build with missing inputs would strand
  the downstream app service.

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
