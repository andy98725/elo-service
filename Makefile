.PHONY: fresh up down build logs clean

run: down build up logs

up:
	docker-compose up -d

down:
	docker-compose down

build:
	docker-compose build

logs:
	docker-compose logs -f

clean:
	docker-compose down -v
	docker system prune -f 

pg-local:
	docker exec -it $$(docker ps -q -f name=postgres) psql -U goserv -d postgres
redis-local:
	redis-cli -h localhost -p 6379

pg-stg:
	fly postgres connect -a elo-service-stg-db
redis-stg:
	fly redis connect