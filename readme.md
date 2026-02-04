


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
Populate the .env with something like
```

```
Then run
```
go run ./src
```

Connect locally to postgres
```
docker exec -it $(docker ps -q -f name=postgres) psql -U goserv -d postgres
```

To connect to redis
```
redis-cli -h localhost -p 6379
```


## Staging
To connect to fly.io
```
flyctl auth login
```

To connect to postgres
```
fly postgres connect -a elo-service-stg-db
```

To connect to redis
```
fly redis connect
```
