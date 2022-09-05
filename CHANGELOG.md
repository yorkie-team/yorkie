# Changelog

All notable changes to Yorkie will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and Yorkie adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.18] - 2022-09-05

### Added

- Bind project and user with owner field: #398
- Validate fields when creating or updating a project: #399

## [0.2.17] - 2022-08-25

### Added

- Add `--backend-snapshot-with-purging-changes` flag: #370

### Fixed

- Fix history command authentication error: #397

## [0.2.16] - 2022-08-16

### Added

- Add `--auth-webhook-url`, `--name` flag to updateProject command: #376

### Changed

- Rename package names to match JS SDK: #395

## [0.2.15] - 2022-08-08

### Added

- Introduce `buf` to enforce lint rules designed to guarantee consistency: #382
- Extract admin settings with flags and configurations: #384

### Changed

- Change uint64 to int64 among data inserted into the DB: #381
- Add `[jstype = JS_STRING]` field option in admin.proto: #380

## [0.2.14] - 2022-08-03

### Added

- Add signup and login commands and APIs: #357

### Fixed

- Fix the problem local changes were applied twice: #375

## [0.2.13] - 2022-07-27

### Added

- Add document list command to CLI: #366
- Add SearchDocuments admin API: #363

## [0.2.12] - 2022-07-20

### Fixed

- Fix incorrect index for nodes newly created then concurrently removed: #364

## [0.2.11] - 2022-07-14

### Added

- Apply gRPC error details to update project api: #354
- Implement pagination flags to history command: #360

## [0.2.10] - 2022-07-06

### Added

- Add MaxCallRecvMsgSize option to client: #353

### Changed

- Improve performance of deletion in Text: #356

### Fixed

- Fix a bug when deleting blocks concurrently: #b645cf1

## [0.2.9] - 2022-06-30

### Added

- Add history command to CLI: #349
- Introduce validator for project name: #345

### Fixed

- Revert text delection improvements: #350

## [0.2.8] - 2022-06-22

### Added

- Add UpdateProject admin API: #338

### Changed

- Improve performance of deletion in Text: #341

## [0.2.7] - 2022-06-14

### Fixed

- Expose the missing exit code: #e87d9d3
- Skip storing the initial ticket to prevent GC interruption: #339
- Cache removed elements when creating a document from a snapshot: #340
- Detach the attached documents when deactivating the client: #342

## [0.2.6] - 2022-05-25

### Changed

- Update Go version to 1.18: #326
- Add trylock to memory locker: #234
- Print projects in table format: #324
- Fetch documents by project: #330
- Add time attributes to document API: #325

### Fixed

- Fix invalid JSON returns from document.Marshal: #328, #332

## [0.2.5] - 2022-05-11

### Added

- Add the missing admin-port flag for CLI

### Fixed

- Rename projectID written in invalid conventions to project_id

## [0.2.4] - 2022-05-10

### Added

- Add Project(Multi-tenancy): #313, #319
- Add Admin API(ListDocuments, GetDocument, ListProjects): #309, #312, #315, #320

### Changed

- Cache ActorID.String to reduce memory usage: #308
- Rename Agent to Server: #311
- Rename Client Metadata to Presence: #323

### Fixed

- Fix LRU cache expiration when updating same key: #314

### Removed

- Remove collection from document: #318

## [0.2.3] - 2022-04-07

### Changed

- Introduce named logging to separate logs by request or routine: #296
- Add missing serverSeq of change.ID in Protobuf: #f5a0c49
- Cache the key of RGATreeSplitNodeID to prevent instantiation: #306
- Cache the key of TimeTicket to prevent instantiation: #307

### Fixed

- Fix for use on Windows: #295
- Fix snapshot interval to make them trigger properly in memdb: #297
- Run tests using monkey patch only on amd64: #299
- Fix a warning that directory does not exist when running make proto: #c441b7b

## [0.2.2] - 2022-01-24

### Added

- Add log-level flag: #290

### Fixed

- Fix a bug that reads config file incorrectly: #4cf184d
- Calculate minSyncedTicket based on time.Ticket: #289

## [0.2.1] - 2022-01-04

### Added

- Allow users to set up logger of the client: #285
- Housekeeping to deactivate clients that have not been updated: #286
- Run GC when saving snapshots: #287

### Changed

- Clean up client options: #284

2nd year release

## [0.2.0] - 2021-12-19

2nd year release

### Added

- Monitoring #155
- Supporting TLS and Auth webhook #6
- Providing Cluster Mode #11
- Improved Peer Awareness #153
- Providing MemoryDB for Agent without MongoDB #276

### Changed

### Removed

### Deprecated

## [0.1.12] - 2021-12-04

### Fixed

- Fix a bug to pull changes from other documents in MemDB: #9c2af2e

## [0.1.11] - 2021-12-04

### Added

- Add MemDB to run Yorkie without MongoDB: #276
- Add rpc-max-requests-bytes flag to set client request limit: #e544cdb

### Changed

- Avoid creating snapshots of a document at the same time in ETCD: #274
- Extract auth-webhook-cache-size as config and flag: #3256b95

### Fixed

- Fix a bug where text nodes with tombstones were not counted: #277

## [0.1.10] - 2021-11-16

### Added

- Add enable-pprof flag to open pprof via profiling server: #265
- Build multiple architecture docker images: #10d8c8b
- Add operations metrics in PushPull API: #d23fc14

### Changed

- Replace XXXGauges with XXXCounters: #266
- Reduce memory usage in PushPull API: #268

### Fixed

- Fix goroutine leaks on subscriptions: #265
- Add missing go process collector on metrics: #35cefdb
- Fix missing gRPC interceptors for metrics: #901e4fa

## [0.1.9] - 2021-10-31

### Added

- Add ETCD username and password flags: #259

### Changed

- Change the flag missed when renaming to AuthWebHookXXX: #feb831d

### Fixed

- Fix a bug that gRPC metrics were not displayed: #02c1995
- Fix a bug that Go process metrics were not displayed: #262

## [0.1.8] - 2021-10-21

### Fixed

- Revert "Replace hex.EncodeTostring with ActorID.key in Text and RichText (#255)"

## [0.1.7] - 2021-10-19

### Added

- Improve Client's metadata to be updatable: #153
- Build binaries for environments when releasing a new version: #175

### Changed

- Add config validation: #206
- Clean up flags in AuthXXX and XXXSec patterns: #168
- Update MaxConcurrentStreams to max: #227
- Clear performance bottlenecks: #251
- Change config format to YAML: #223

### Fixed

- Fix reduce array size when deleting the same position: #235
- Fix invalid version package path: #241
- Add registry missing in PR 185 to Metrics: #e65d5bb

## [0.1.6] - 2021-07-24

### Added

- Add Cluster Mode: #183
- Add Authorization check to Watch API: #209
- Add authorization-webhook-methods flag: #193
- Add retry logic to Authorization webhook: #194
- Add authorization webhook cache: #192

### Changed

- Change Watch events to be similar to JS SDK: #137
- Close Watch streams on agent shutdown: #208

### Fixed

- Fix a bug where deleted values from objects are revivded after GC: #202

## [0.1.5] - 2021-06-05

### Added

- Add basic behavior of authorization webhook: #188

### Fixed

- Fix the concurrent editing issue of Move Operation: #196

### Removed

- Delete RequestHeader in Protobuf: #188

## [0.1.4] - 2021-05-15

### Added

- Add gRPC health checking: #176
- Add ca-certificates to access remote DBs such as MongoDB Atlas: #6d3e176

### Changed

- Expose only basic command in Dockerfile: #317320b

### Fixed

- Fix incorrect sequences when detaching documents: #173

## [0.1.3] - 2021-04-04

### Added

- Add more metrics related to PushPull API: #166
- Add command-line flags for agent command: #167
- Support for null values: #160

### Changed

- Update Go version to 1.16: #161
- Calculate the size of Text in UTF-16 code units: #165

### Fixed

- Fix invalid states of SplayTree: #162
- Remove errors that occur when insPrev does not exist: #164

## [0.1.2] - 2021-02-14

### Added

- Add customizable metadata for peer awareness: #138

### Changed

- Use Xid instead of UUID for SubscriptionID: #142
- Replace the type of client_id to a byte array to reduce payload: #145

### Fixed

- Fix actorID loss while converting Change to ChangeInfo: #144

## [0.1.1] - 2021-01-01

### Added

- Add version tag when pushing image: #102
- Add garbage collection for TextElement: #104

### Changed

- Use multi-stage build for docker image: #107
- Wrap additional information to sentinel errors: #129
- Use status for one-dimensional value: #131
- Remove panics in the converter: #132, #135

### Fixed

- Fix check already attached document: #111
- Delete removed elements from the clone while running GC: #103
- Fix to use internal document when pulling snapshot from agent: #120

## [0.1.0] - 2020-11-07

First public release

### Added

- Add basic structure of Yorkie such as `Document`, `Client`, and Agent(`yorkie`)
- Add Custom CRDT data type `Text` for code editor: #2
- Add Snapshot API to reduce payload: #9
- Add Garbage Collection to clean CRDT meta: #3
- Add Custom CRDT data type `RichText` for WYSIWYG editor: #7
- Add Peer Awareness API: #48
- Add Prometheus metrics: #76
- Add Custom CRDT data type `Counter`: #82

### Changed

### Removed

### Deprecated

[unreleased]: https://github.com/yorkie-team/yorkie/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/yorkie-team/yorkie/releases/tag/v0.1.0#
