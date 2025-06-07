


### Notes

Connect locally to postgres

```
docker ps
docker exec -it <postgres_container> psql -U goserv -d postgres
```


## FlyCTL
To connect to postgres
```
flyctl auth login
fly postgres connect -a elo-service-stg-db
```
To initialize tables, `./dbsetup.sql`