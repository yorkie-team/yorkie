# Yorkie

[![Github](https://img.shields.io/github/stars/yorkie-team/yorkie.svg?style=social)](https://github.com/yorkie-team/yorkie)
[![Twitter](https://img.shields.io/twitter/follow/team_yorkie.svg?label=Follow)](https://twitter.com/team_yorkie)
[![Slack](https://img.shields.io/badge/chat-on%20Slack-brightgreen.svg?style=social&amp;logo=slack)](https://dev-yorkie.slack.com/)
[![Contributors](https://img.shields.io/github/contributors/yorkie-team/yorkie.svg)](https://github.com/yorkie-team/yorkie/contributors)
[![Commits](https://img.shields.io/github/commit-activity/m/yorkie-team/yorkie.svg)](https://github.com/yorkie-team/yorkie/pulse)

[![Go Report Card](https://goreportcard.com/badge/github.com/yorkie-team/yorkie)](https://goreportcard.com/report/github.com/yorkie-team/yorkie)
[![CodeCov](https://img.shields.io/codecov/c/github/yorkie-team/yorkie)](https://codecov.io/gh/yorkie-team/yorkie)

Yorkie is an open source document store for building collaborative editing applications.

Yorkie consists of three main components: Client, Document and Agent.

 ```
  +--Client "A" (Go)----+
  | +--Document "D-1"-+ |               +--Agent------------------+
  | | { a: 1, b: {} } | <-- Changes --> | +--Collection "C-1"---+ |
  | +-----------------+ |               | | +--Document "D-1"-+ | |      +--Mongo DB--+
  +---------------------+               | | | { a: 1, b: {} } | | |      | Changes    |
                                        | | +-----------------+ | | <--> | Snapshot   |
  +--Client "B" (JS)----+               | | +--Document "D-2"-+ | |      +------------+
  | +--Document "D-1"-+ |               | | | { a: 1, b: {} } | | |
  | | { a: 2, b: {} } | <-- Changes --> | | +-----------------+ | |
  | +-----------------+ |               | +---------------------+ |
  +---------------------+               +-------------------------+
                                                     ^
  +--Client "C" (JS)------+                          |
  | +--Query "Q-1"------+ |                          |
  | | db.['C-1'].find() | <-- MongoDB query ---------+
  | +-------------------+ |
  +-----------------------+
 ```

 - Clients can have a replica of the document representing an application model locally on several devices.
 - Each client can independently update the document on their local device, even while offline.
 - When a network connection is available, the client figures out which changes need to be synced from one device to another, and brings them into the same state.
 - If the document was changed concurrently on different devices, Yorkie automatically syncs the changes, so that every replica ends up in the same state with resolving conflict.

## Agent and SDKs

 - Agent: https://github.com/yorkie-team/yorkie
 - JS SDK: https://github.com/yorkie-team/yorkie-js-sdk
 - Go Client: https://github.com/yorkie-team/yorkie/tree/master/client

## Quick Start

For now, we didn't deploy binary yet. So you should [compile Yorkie yourself](#developing-yorkie).

Yorkie uses MongoDB to store it's data. To start MongoDB, type `cd docker && docker-compose up -d`.

Next, let's start a Yorkie agent. Agent runs until they're told to quit and handle the communication of maintenance tasks of Agent. and start the agent:

```
$ ./bin/yorkie agent
```

Use the -c option to change settings such as the MongoDB connectionURI.

```
$ ./bin/yorkie agent -c yorkie.json
```

The configuration file with default values is shown below.

https://github.com/yorkie-team/yorkie/blob/master/yorkie/config.sample.json

## Documentation

Full, comprehensive documentation is viewable on the Yorkie website:

https://yorkie.dev/docs/master/

## Developing Yorkie

For building Yorkie, You'll first need [Go](https://golang.org) installed (version 1.13+ is required). Make sure you have Go properly [installed](https://golang.org/doc/install), including setting up your [GOPATH](https://golang.org/doc/code.html#GOPATH). Then download a pre-built binary from [release page](https://github.com/protocolbuffers/protobuf/releases) and install the protobuf compiler.

We need to install Golang packages to build Yorkie locally. You can run `make tools` to install the required packages.

Next, clone this repository into some local directory and then just type `make build`. In a few moments, you'll have a working `yorkie` executable:
```
$ make build
...
$ bin/yorkie
```

Tests can be run by typing `make test`.

*NOTE: `make test` includes integration tests that require a local MongoDB listen on port 27017. To start MongoDB, type `docker-compose up`.*

If you make any changes to the code, run `make fmt` in order to automatically format the code according to Go [standards](https://golang.org/doc/effective_go.html#formatting).

## Contributing
See [CONTRIBUTING](CONTRIBUTING.md) for details on submitting patches and the contribution workflow.

## Sponsors

Is your company using Yorkie? Ask your boss to support us. It will help us dedicate more time to maintain this project and to make it even better for all our users. Also your company logo will show up on here and on our website :-) [[Become a sponsor](https://opencollective.com/yorkie#sponsor)]
<a href="https://opencollective.com/yorkie#sponsor" target="_blank"><img src="https://opencollective.com/yorkie/sponsor.svg?width=890"></a>

### Backers
Please be our [Backers](https://opencollective.com/yorkie#backers).
<a href="https://opencollective.com/yorkie#backers" target="_blank"><img src="https://opencollective.com/yorkie/backers.svg?width=890"></a>
