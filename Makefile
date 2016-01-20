all: build push
build:
	docker build --no-cache=true -t always-https .
	docker tag -f always-https docker.sunet.se/always-https
update:
	docker build -t always-https .
	docker tag -f always-https docker.sunet.se/always-https
push:
	docker push docker.sunet.se/always-https	
