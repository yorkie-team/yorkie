# Contributing

Yorkie is Apache 2.0 licensed and accepts contributions via GitHub pull requests. This document outlines some conventions on commit message formatting, contact points for developers, and other resources to help get contributions into Yorkie.

## Contacts

If you have any questions along the way, please don’t hesitate to ask us

- Discord: [Yorkie Discord](https://discord.com/invite/MVEAwz9sBy).

## Contribution Flow

This is a rough outline of a contributor's workflow:

1. Install prerequisites for developing Yorkie.
2. Create a personal fork of the original repository.
3. Clone the fork to your local working directory.
4. Set environment for building and testing.
5. Create a topic branch from where to base the contribution (usually main).
6. Make commits of logical units.
   - Make sure all tests pass, and add any new tests as appropriate.
   - Make sure commit messages are in the proper format (see below).
7. Push changes in a topic branch to the forked repository.
8. Submit a pull request to the original repository.
9. Address any review comments from CodeRabbit and maintainers.
10. After receiving LGTM from maintainers, the PR will be merged.

Thanks for contributing!

## Setting Developing Environment

### Requirements

Below are needed for developing and building Yorkie.

- [Go](https://golang.org) (version 1.18+)
- [Protobuf Compiler](https://github.com/protocolbuffers/protobuf/releases) (version 3.4.0+)
- [Docker](https://www.docker.com/)

### Building & Testing

You must install required Go packages to build Yorkie executable.

```sh
make tools
make build		# executable: ./bin/yorkie
```

You can set testing environment via Docker Compose. It is needed because integration tests require local applications like MongoDB.

```sh
docker compose -f build/docker/docker-compose.yml up --build -d
make test
```

You can automatically check the programmatic and stylistic errors of your code.

```sh
make lint
```

> [!NOTE]  
> If you have an old version of `golangci-lint` installed locally, running `make lint` may fail—especially if the linter doesn't support the newer version of Go. It's recommended to run `make tools` periodically to keep your tools up to date.

### Setting for VSCode
If you are using VSCode, add the following in your `.vscode/settings.json` so that proper language features work correctly in test files
with build tag `integration`, `bench`, or `complex`.
```json
{
  "gopls": {
    "build.buildFlags": ["-tags=integration,bench,complex"]
  }
}
```

## Design Documents

For developers, [design documents](design/README.md) about core features are provided. You can refer to the docs for understanding the overall structure of Yorkie.

## Conventions

### Code Style

The coding style suggested by the Golang community is used in Yorkie. See the [style doc](https://github.com/golang/go/wiki/CodeReviewComments) for details. We also recommended referring to [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md).

If you make any changes to the code, run `make fmt` to automatically format the code according to Go [standards](https://golang.org/doc/effective_go.html#formatting).

### Commit Message Format

We follow a rough convention for commit messages that is designed to answer two questions: what changed and why. The subject line should feature the what and the body of the commit should describe the why.

```
Remove the synced seq when detaching the document

To collect garbage like CRDT tombstones left on the document, all
the changes should be applied to other replicas before GC. For this
, if the document is no longer used by this client, it should be
detached.
```

The first line is the subject and should be no longer than 70 characters, the second line is always blank, and other lines should be wrapped at 80 characters. This allows the message to be easier to read on GitHub as well as in various git tools.

### Testing

Testing is the responsibility of all contributors, but it is also coordinated by maintainers. It is recommended to write them in order from successful cases to exceptional cases.

There are multiple types of tests. The location of the test code varies with type, as do the specifics of the environment needed to successfully run the test:

- Unit: These confirm that a particular function behaves as intended.
- Integration: These tests cover interactions of package components or interactions between Yorkie packages and some other non-Yorkie system resource (eg: MongoDB).
- Benchmark: These confirm that the performance of the implemented function.

### Code Coverage

We are using [Codecov](https://about.codecov.io) for analyzing PR's code coverage. If you want to check the coverage of your code in local browser, you can run the command below.

```sh
make coverage
```

### Code Review

We use GitHub's pull request review feature to review code. We also use CodeRabbit to automate code review for reducing the effort of maintainers. CodeRabbit will automatically review your pull request and provide feedback on code quality, test coverage, and more.

We require that all code is reviewed by at least one maintainer before being merged. We may ask for changes to be made to the code before it is merged.

## Contributor License Agreement (CLA)

We require that all contributors sign our Contributor License Agreement ("CLA") before we can accept the contribution.

### Signing the CLA

Open a pull request ("PR") to any of our open source projects to sign the CLA. A bot will comment on the PR asking you to sign the CLA if you haven't already.

Follow the steps given by the bot to sign the CLA. This will require you to log in with GitHub. We will only use this information for CLA tracking. You only have to sign the CLA once. Once you've signed the CLA, future contributions to the project will not require you to sign again.

If the bot still require you to sign the CLA although you had already signed, you can check your commit's author is equal to the Github account that you logged in.

### Why Require a CLA?

Agreeing to a CLA explicitly states that you are entitled to provide a contribution, that you cannot withdraw permission to use your contribution at a later date, and that Yorkie Team has permission to use your contribution.

This removes any ambiguities or uncertainties caused by not having a CLA and allows users and customers to confidently adopt our projects. At the same time, the CLA ensures that all contributions to our open source projects are licensed under the project's respective open source license, such as Apache-2.0 License.

Requiring a CLA is a common and well-accepted practice in open source. Major open source projects require CLAs such as Apache Software Foundation projects, Facebook projects, Google projects, Python, Django, and more. Each of these projects remains licensed under permissive OSS licenses such as MIT, Apache, BSD, and more.
