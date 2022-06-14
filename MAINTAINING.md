# Maintaining Yorkie

## Releasing a New Version

### Updating and Deploying Yorkie

1. Update YORKIE_VERSION in [Makefile](https://github.com/yorkie-team/yorkie/blob/main/Makefile#L1).
2. Write changelog of this version in [CHANGELOG.md](https://github.com/yorkie-team/yorkie/blob/main/CHANGELOG.md).
3. Create Pull Request and merge it into main.
4. Build binaries to attach a new release with `make build-binaries`
5. Create [a new release](https://github.com/yorkie-team/yorkie/releases/new) by attaching the changelog created in step 2 and binaries created in step 4.
6. Then [GitHub action](https://github.com/yorkie-team/yorkie/blob/main/.github/workflows/docker-publish.yml) will deploy Yorkie to [Docker Hub](https://hub.docker.com/repository/docker/yorkieteam/yorkie).
7. Create a PR to [homebrew-core](https://github.com/Homebrew/homebrew-core) by typing `brew bump-formula-pr yorkie --tag v{YORKIE_VERSION}` in the terminal.
