


# Overview

The code in the root level of this repo is for the elo-matchmaking service.
There is currently only a staging environment.

It connects to Postgres for persistent data (Users, game records, ratings, etc.) and Redis for transient data (matchmaking, lobbies, etc.).

It also runs worker threads that read off Redis to spawn Docker Images on [SERVICE].

The elo-matchmaking service is hosted on [fly.io](https://fly.io/apps/elo-service).

Postgres DB is hosted on [Neon](https://console.neon.tech/app/projects/old-shadow-44280217/branches/br-nameless-feather-afzllkpa/).

Redis is hosted on [Upstash](https://console.upstash.com/redis?teamid=0).


# Environments


## Local
Run
```
make up
```

Connect locally to postgres
```
docker exec -it $(docker ps -q -f name=postgres) psql -U goserv -d postgres
```

To connect to redis
```
redis-cli -h localhost -p 6379
```

To connect to staging locally, populate the .env file, then run
```
go run ./src
```


## Staging

To connect to fly.io
```
flyctl auth login
```

To connect to postgres, use the [dashboard](https://console.neon.tech/app/projects/old-shadow-44280217/branches/br-nameless-feather-afzllkpa/tables)
or get the connection details from the [branch overview](https://console.neon.tech/app/projects/old-shadow-44280217/branches/br-nameless-feather-afzllkpa?branchId=br-nameless-feather-afzllkpa&database=neondb)

To connect to redis, use the [dashboard](https://console.upstash.com/redis/22ddac14-a684-4547-9b8e-a7ec313da40f/cli?teamid=0)
or get the connection details under Endpoint on the [details page](https://console.upstash.com/redis/22ddac14-a684-4547-9b8e-a7ec313da40f/details?teamid=0)

For hetzner, see the 