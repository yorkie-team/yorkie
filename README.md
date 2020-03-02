# Yorkie

Yorkie is a framework for building collaborative editing applications.

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
 - When a network connection is available, Yorkie figures out which changes need to be synced from one device to another, and brings them into the same state.
 - If the document was changed concurrently on different devices, Yorkie automatically syncs the changes, so that every replica ends up in the same state with resolving conflict.

## Agent and SDKs

 - Agent: https://github.com/yorkie-team/yorkie
 - JS SDK: https://github.com/yorkie-team/yorkie-js-sdk
 - Go Client: https://github.com/yorkie-team/yorkie/tree/master/client

## Quick Start

For now, we didn't deploy binary yet. So you should [compile Yorkie yourself](#developing-yorkie).

Yorkie uses MongoDB to store it's data. To start MongoDB, type `docker-compose up`.

Next, let's start a Yorkie agent. Agent runs until they're told to quit and handle the communication of maintenance tasks of Agent. and start the agent:

```
$ ./bin/yorkie agent
```

Use the -c option to change settings such as the MongoDB connectionURI.

```
$ ./bin/yorkie agent -c yorkie.json
```

The configuration file with default values is shown below.

```
# yorkie.json
{
   "RPCPort":9090,
   "Mongo":{
      "ConnectionURI":"mongodb://mongo:27017",
      "ConnectionTimeoutSec":5,
      "PingTimeoutSec":5,
      "YorkieDatabase":"yorkie-meta"
   }
}
```

## Documentation

Full, comprehensive documentation is viewable on the Yorkie website:

https://yorkie.dev/docs/master/

## Developing Yorkie

For building Yorkie, You'll first need [Go](https://golang.org) installed (version 1.13+ is required). Make sure you have Go properly [installed](https://golang.org/doc/install), including setting up your [GOPATH](https://golang.org/doc/code.html#GOPATH).

Next, clone this repository into some local directory and then just type `make build`. In a few moments, you'll have a working `yorkie` executable:
```
$ make
...
$ bin/yorkie
```

Tests can be run by typing `make test`.

*NOTE: `make test` includes integration tests that require a local MongoDB listen on port 27017. To start MongoDB, type `docker-compose up`.*

If you make any changes to the code, run `make fmt` in order to automatically format the code according to Go [standards](https://golang.org/doc/effective_go.html#formatting).
