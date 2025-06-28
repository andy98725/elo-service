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
	cd example-game-server && docker build -t example-server .
	docker run -p 8080:8080 -p 8081:8081 example-server -token "abc123" "player1" "player2"
	# To query health: curl http://localhost:8080/health
	# To join players: curl -X POST http://localhost:8080/join -H "Content-Type: text/plain" -d "player1"

example-push:
	cd example-game-server && docker build -t example-server .
	docker tag example-server andy98725/example-server:latest
	docker push andy98725/example-server:latest
