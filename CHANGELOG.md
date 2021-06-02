# Changelog

All notable changes to Yorkie will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and Yorkie adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/yorkie-team/yorkie/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/yorkie-team/yorkie/releases/tag/v0.1.0# 
