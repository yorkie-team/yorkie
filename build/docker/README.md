# Docker Compose Files

[Docker Compose](https://docs.docker.com/compose/) is a tool for defining and
running multi-container Docker applications. We use Docker Compose to run the
applications needed during Yorkie development.

When developing Yorkie, we can easily run the required dependant applications
through `docker compose` command.

```bash
# Run docker compose up and Compose starts and runs apps.
docker compose -f build/docker/docker-compose.yml up --build -d

# Shut down the apps
docker compose -f build/docker/docker-compose.yml down
```

The docker-compose files we use are as follows:
- `docker-compose.yml`: This file is used to run Yorkie's integration tests. It
 runs MongoDB.
- `docker-compose-full.yml`: This file builds Yorkie and launches it. It also runs
 MongoDB and monitoring tools such as Prometheus and Grafana.

Both files use the Compose project name `yorkie`, so containers are named
`yorkie-<service>-<n>` (e.g. `yorkie-mongo-1`); the sharding and analytics
stacks use `yorkie-sharding` and `yorkie-analytics`. Address a container by
its service name with `docker compose -f <file> exec <service> ...`.

MongoDB 8 does not start on Linux kernels 6.19 through 7.0.13
([SERVER-125742](https://jira.mongodb.org/browse/SERVER-125742)). Containers
use the Docker VM's kernel, so check it with
`docker info --format '{{.KernelVersion}}'`. Docker Desktop 4.87.0 and later
ship 7.0.12; use a kernel below 6.19 (Docker Desktop 4.86.0 or earlier) or
7.0.14 and later.

## Subdirectories

- [analytics/](./analytics/README.md): Compose stack for analytics (Kafka, StarRocks)
- [monitoring/](./monitoring/README.md): Compose stack for monitoring (Prometheus, Grafana)
- [sharding/](./sharding/README.md): Compose stack for sharded MongoDB topology
