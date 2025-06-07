


### Notes

Connect locally to postgres

```
docker exec -it $(docker ps -q -f name=postgres) psql -U goserv -d postgres
```


## FlyCTL
To connect to postgres
```
flyctl auth login
fly postgres connect -a elo-service-stg-db
```

# TODO

[ ] Add Games, Matchmaking