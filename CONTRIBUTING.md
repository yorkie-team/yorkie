## How to contribute

Yorkie is Apache 2.0 licensed and accepts contributions via GitHub pull requests. This document outlines some of the conventions on commit message formatting, contact points for developers, and other resources to help get contributions into Yorkie.

### Contacts

If you have any questions along the way, please don’t hesitate to ask us
- Slack: [Dev Yorkie](https://dev-yorkie.slack.com/): You can sign up for our [Slack here](https://communityinviter.com/apps/dev-yorkie/yorkie-framework).

### Getting started

- Fork the repository on GitHub
- Read the README.md for build instructions

## Contribution flow

This is a rough outline of what a contributor's workflow looks like:

- Create a topic branch from where to base the contribution. This is usually master
- Make commits of logical units
- Make sure commit messages are in the proper format
- Push changes in a topic branch to a personal fork of the repository
- Submit a pull request to yorkie-team/yorkie
- The PR must receive a LGTM from maintainers

Thanks for contributing!

### Code style

The coding style suggested by the Golang community is used in Yorkie. See the [style doc](https://github.com/golang/go/wiki/CodeReviewComments) for details.

### Format of the commit message

We follow a rough convention for commit messages that is designed to answer two questions: what changed and why. The subject line should feature the what and the body of the commit should describe the why.

```
Remove the synced seq when detaching the document

To collect garbage like CRDT tombstones left on the document, all
the changes should be applied to other replicas before GC. For this
, if the document is no longer used by this client, it should be
detached.
```

The first line is the subject and should be no longer than 70 characters, the second line is always blank, and other lines should be wrapped at 80 characters. This allows the message to be easier to read on GitHub as well as in various git tools.
