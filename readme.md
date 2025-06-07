


# Environments


## Local

Connect locally to postgres
```
docker exec -it $(docker ps -q -f name=postgres) psql -U goserv -d postgres
```

To connect to redis
```
redis-cli -h localhost -p 6379
```


## Staging (FlyCTL)
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

# TODO

[ ] Add Games, Matchmaking