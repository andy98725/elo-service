


### Notes

Connect locally to postgres

```
psql -U postgres -d postgres
```


## FlyCTL
To connect to postgres
```
flyctl auth login
fly postgres connect -a elo-service-stg-db
```
To initialize tables, `./dbsetup.sql`