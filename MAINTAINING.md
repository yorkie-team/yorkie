# Maintaining Yorkie

## Releasing a New Version

### Updating and Deploying Yorkie

1. Update YORKIE_VERSION in [Makefile](https://github.com/yorkie-team/yorkie/blob/main/Makefile#L1).
2. Write changelog of this version in [CHANGELOG.md](https://github.com/yorkie-team/yorkie/blob/main/CHANGELOG.md).
3. Create Pull Request and merge it into main.
4. Create [a new release](https://github.com/yorkie-team/yorkie/releases/new) by attaching the changelog created in step 2.
5. Then [GitHub action](https://github.com/yorkie-team/yorkie/blob/main/.github/workflows/docker-publish.yml) will deploy Yorkie to [Docker Hub](https://hub.docker.com/repository/docker/yorkieteam/yorkie).
