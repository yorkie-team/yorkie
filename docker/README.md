# Docker Compose Files

[Docker Compose](https://docs.docker.com/compose/) is a tool for defining and
running multi-container Docker applications. We use Docker Compose to run the
applications needed during Yorkie development.

When developing Yorkie, we can easily run the required dependant applications
through `docker-compose` command.

```bash
# Run docker-compose up and Compose starts and runs apps.
docker-compose -f docker/docker-compose.yml up --build -d

# Shut down the apps
docker-compose -f docker/docker-compose.yml down
```

The docker-compose files we use are as follows:
- `docker-compose.yml`: This file is used to run Yorkie's integration tests. It
 runs MongoDB and etcd.
- `docker-compose-full.yml`: This file launches all the applications needed to
 develop Yorkie. It also runs monitoring tools such as Prometheus and Grafana.
