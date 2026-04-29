# Yorkie

[![GitHub](https://img.shields.io/github/stars/yorkie-team/yorkie.svg?style=social)](https://github.com/yorkie-team/yorkie)
[![Twitter](https://img.shields.io/twitter/follow/team_yorkie.svg?label=Follow)](https://twitter.com/team_yorkie)
[![Discord](https://img.shields.io/discord/928301813785038878?label=discord&logo=discord&logoColor=white)](https://discord.gg/MVEAwz9sBy)
[![Contributors](https://img.shields.io/github/contributors/yorkie-team/yorkie.svg)](https://github.com/yorkie-team/yorkie/contributors)
[![Commits](https://img.shields.io/github/commit-activity/m/yorkie-team/yorkie.svg)](https://github.com/yorkie-team/yorkie/pulse)

[![Build Status](https://github.com/yorkie-team/yorkie/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/yorkie-team/yorkie/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/yorkie-team/yorkie)](https://goreportcard.com/report/github.com/yorkie-team/yorkie)
[![CodeCov](https://img.shields.io/codecov/c/github/yorkie-team/yorkie)](https://codecov.io/gh/yorkie-team/yorkie)
[![Godoc](http://img.shields.io/badge/go-documentation-blue.svg?style=flat-square)](https://godoc.org/github.com/yorkie-team/yorkie)

Yorkie is an open-source document store for building real-time collaborative applications. It uses JSON-like documents(CRDT) with optional types.

Yorkie consists of three main components: Client, Document and Server.

```
 Client "A" (Go)               Server(Cloud or Self-Hosted)  MongoDB or MemDB
┌───────────────────┐           ┌────────────────────────┐   ┌───────────┐
│  Document "D-1"   │◄─Changes─►│  Project "P-1"         │   │ Changes   │
│  { a: 1, b: {} }  │           │ ┌───────────────────┐  │◄─►│ Snapshots │
└───────────────────┘           │ │  Document "D-1"   │  │   └───────────┘
 Client "B" (JS)                │ │  { a: 2, b: {} }  │  │
┌───────────────────┐           │ │                   │  │
│  Document "D-1"   │◄─Changes─►│ │  Document "D-2"   │  │
│  { a: 2, b: {} }  │           │ │  { a: 3, b: {} }  │  │
└───────────────────┘           │ └───────────────────┘  │
 Dashboard or CLI               └────────────────────────┘
┌────────────────────┐              ▲
│  Query "Q-1"       │              |
│  P-1.find({a:2})   ├───── Query───┘
└────────────────────┘
```

Key Features:

- Clients: Clients can have a local replica of the document representing an application model on several devices.
- Offline Editing: Each client can independently update the document on their local device, even while offline.
- Synchronization: When a network connection is available, the client figures out which changes need to be synced from one device to another, and brings them into the same state.
- Conflict Resolution: If the document was changed concurrently on different devices, Yorkie automatically syncs the changes, so that every replica ends up in the same state with resolving conflicts.
- Database Integration: Yorkie supports MongoDB and MemDB as the underlying data storage.

## Documentation

Full, comprehensive [documentation](https://yorkie.dev/docs) is available on the Yorkie website.

### Getting Started

- [with JS SDK](https://yorkie.dev/docs/getting-started/with-js-sdk)
- [with iOS SDK](https://yorkie.dev/docs/getting-started/with-ios-sdk)
- [with Android SDK](https://yorkie.dev/docs/getting-started/with-android-sdk)

## Project Documentation

- [docs/design/](docs/design/README.md) — Architectural design documents
- [docs/tasks/](docs/tasks/README.md) — Task tracking (active and archived)
- [test/k6/](test/k6/README.md) — Load testing setup
- [build/charts/](build/charts/README.md) — Helm charts (cluster, monitoring, analytics)
- [build/docker/](build/docker/README.md) — Docker Compose files for local dev
- [CHANGELOG.md](CHANGELOG.md) — Release notes
- [ROADMAP.md](ROADMAP.md) — Roadmap
- [MAINTAINING.md](MAINTAINING.md) — Release and maintenance procedures
- [CLAUDE.md](CLAUDE.md) — Agent instructions for AI-assisted development

## Contributing

See [CONTRIBUTING](CONTRIBUTING.md) for details on submitting patches and the contribution workflow.

## Contributors ✨

Thanks go to these incredible people:

<a href="https://github.com/yorkie-team/yorkie/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=yorkie-team/yorkie" alt="contributors"/>
</a>

## Sponsors

Is your company using Yorkie? Ask your boss to support us. It will help us dedicate more time to maintain this project and to make it even better for all our users. Also, your company logo will show up on here and on our website: -) [[Become a sponsor](https://opencollective.com/yorkie#sponsor)]
<a href="https://opencollective.com/yorkie#sponsor" target="_
blank"><img src="https://opencollective.com/yorkie/sponsor.svg?width=890"></a>

### Backers

Please be our [Backers](https://opencollective.com/yorkie#backers).
<a href="https://opencollective.com/yorkie#backers" target="_blank"><img src="https://opencollective.com/yorkie/backers.svg?width=890"></a>
