# Maintaining Yorkie

## Releasing a New Version

### Updating and Deploying Yorkie

1. Update YORKIE_VERSION in [Makefile](https://github.com/yorkie-team/yorkie/blob/main/Makefile#L1).
2. Update `version` and `appVersion` in [Helm Chart](https://github.com/yorkie-team/yorkie/blob/main/build/charts/yorkie-cluster/Chart.yaml#L14-L15).
3. Write changelog of this version in [CHANGELOG.md](https://github.com/yorkie-team/yorkie/blob/main/CHANGELOG.md).
4. Create Pull Request and merge it into main.
5. Build binaries to attach a new release with `make build-binaries`
6. Create [a new release](https://github.com/yorkie-team/yorkie/releases/new) by attaching the changelog by clicking Generate release notes button.
7. Then [GitHub action](https://github.com/yorkie-team/yorkie/blob/main/.github/workflows/docker-publish.yml) will deploy Yorkie to [Docker Hub](https://hub.docker.com/repository/docker/yorkieteam/yorkie).
8. Create a PR to [homebrew-core](https://github.com/Homebrew/homebrew-core) by typing `brew bump-formula-pr yorkie --tag v{YORKIE_VERSION}` in the terminal.
