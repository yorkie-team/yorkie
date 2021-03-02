# Yorkie's Dependent Application Management

When developing Yorkie, you can easily run the required dependency library through `docker-compose`.

### docker-compose.yml
It runs only MongoDB for storing Yorkie's data.

### docker-compose-ci.yml
It runs etcd and MongoDB required for CI.

### docker-compose-full.yml
It runs all the applications you need for Yorkie development.