# Roadmap
This document defines a high-level roadmap for Yorkie development and upcoming releases.
The features and themes included in each milestone are optimistic in the sense that many do not have clear owners yet.
Community and contributor involvement is vital for successfully implementing all desired items for each release.
We hope that the items listed below will inspire further engagement from the community to keep Yorkie progressing and shipping exciting and valuable features.

Any dates listed below and the specific issues that will ship in a given milestone are subject to change but should give a general idea of what we are planning.
We use the [Projects](https://github.com/orgs/yorkie-team/projects) feature in GitHub so look there for the most up-to-date and issue plan.

## Yorkie v1.0

Yorkie v1.0 focuses on improving the stability of the server and client to make Yorkie more reliable.

### 2024

- Support ProseMirror [#257](https://github.com/yorkie-team/yorkie-js-sdk/issues/257)
- Multi-User Undo/Redo [#652](https://github.com/yorkie-team/yorkie/issues/652)
- History API [#52](https://github.com/yorkie-team/yorkie/issues/52)
- REST API [#677](https://github.com/yorkie-team/yorkie/issues/677)
- Improve UI Workflow [#1](https://github.com/yorkie-team/yorkie-ui-poc/issues/1)

### 2023

- Support batch API for document and presence [#442](https://github.com/yorkie-team/yorkie/issues/442)
- Sharding-based cluster mode [#472](https://github.com/yorkie-team/yorkie/issues/472)
- Support MongoDB Sharded Cluster [#673](https://github.com/yorkie-team/yorkie/issues/673)
- Devtools [#688](https://github.com/yorkie-team/yorkie-js-sdk/issues/688)

## Yorkie v0.3

Yorkie v0.3 should provide APIs in the form of service(PaaS) to make it easier for users to Yorkie.

### 2022

 - Renewal of [Homepage](https://github.com/yorkie-team/homepage)
 - Dashboard [Yorkie House](https://github.com/yorkie-team/yorkie-house)
 - Admin API [#273](https://github.com/yorkie-team/yorkie/issues/273)
 - Multi-tenancy [#310](https://github.com/yorkie-team/yorkie/issues/310)
 - Mobile SDKs [#54](https://github.com/yorkie-team/yorkie/issues/54)

## Yorkie v0.2

Yorkie v0.2 should be reliably available for services used in production environments.

### 2021

 - Mar: Monitoring [#155](https://github.com/yorkie-team/yorkie/issues/155)
 - May: Supporting TLS and Auth webhook to secure Yorkie [#6](https://github.com/yorkie-team/yorkie/issues/6)
 - Jun: Providing Cluster Mode [#11](https://github.com/yorkie-team/yorkie/issues/11)
 - Oct: Improved Peer Awareness [#153](https://github.com/yorkie-team/yorkie/issues/153)
 - Nov: Release [CodePair](https://codepair.yorkie.dev/)
 - Dec: Providing MemoryDB for Agent without MongoDB [#276](https://github.com/yorkie-team/yorkie/pull/276)

## Yorkie v0.1

Yorkie's first release version, v0.1, aims to implement the basic features of the document store for building collaborative editing applications.

### 2020

 - Jan: Text datatype for supporting text based collaboration editor [#2](https://github.com/yorkie-team/yorkie/issues/2)
 - Feb: Realtime event stream [#5](https://github.com/yorkie-team/yorkie/issues/5)
 - Mar: https://yorkie.dev
 - Apr: Change hook
 - May: Snapshot to reduce payload [#9](https://github.com/yorkie-team/yorkie/issues/9)
 - Jun: Garbage collection to clean CRDT meta [#3](https://github.com/yorkie-team/yorkie/issues/3)
 - Aug:
   - Peer Awareness [#48](https://github.com/yorkie-team/yorkie/issues/48)
   - Introducing Prometheus metrics [#76](https://github.com/yorkie-team/yorkie/issues/76)
 - Dec: Cleanup such as package dependency cleanup and tests cleanup

### 2019
 - Nov: Start the project with adding basic structure(Agent, Client, Document)
 - Dec: JS-SDK(Client, Document) [yorkie-js-sdk](https://github.com/yorkie-team/yorkie-js-sdk)
