.PHONY: run test build pkg

SERVICE=effective-robot
VERSION=$(shell cat VERSION)
PWD=$(shell pwd)

run: pkg
	docker run --rm -p 8080:8080 ${SERVICE}:${VERSION}

test:
	go test ${PWD}/kitchen

build:
	go build -o bin/effective-robot main.go
	go build -o bin/runner runner/runner.go

pkg:
	docker build -t ${SERVICE}:${VERSION} .

challenge:
	@# cleanup before just incase the user sigkill'd
	@docker kill effective-robot-server &> /dev/null || true
	@docker rm effective-robot-server &> /dev/null || true

	@docker run -d --name effective-robot-server ${SERVICE}:${VERSION} &> /dev/null
	@docker run --rm -it --network container:effective-robot-server ${SERVICE}:${VERSION} bin/runner http://127.0.0.1:8080
	
	@docker kill effective-robot-server &> /dev/null || true
	@docker rm effective-robot-server &> /dev/null || true