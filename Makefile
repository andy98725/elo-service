.PHONY: fresh up down build logs clean example-run example-push

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
	fly postgres connect -a elo-service-stg-db -d elo_service
redis-stg:
	fly redis connect

example-run:
	cd example && docker build -t example-server .
	docker run example-server -token "abc123" "player1" "player2"

example-push:
	cd example && docker build -t example-server .
	docker tag example-server andy98725/example-server:latest
	docker push andy98725/example-server:latest
