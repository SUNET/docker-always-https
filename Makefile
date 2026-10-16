NAME=always-https
VERSION=latest
# Pinned tag tracking the Debian base release, to allow rollback/pinning
BASE=trixie

all: build push
build:
	docker build --no-cache=true -t $(NAME):$(VERSION) .
	docker tag $(NAME):$(VERSION) docker.sunet.se/$(NAME):$(VERSION)
	docker tag $(NAME):$(VERSION) docker.sunet.se/$(NAME):$(BASE)
update:
	docker build -t $(NAME):$(VERSION) .
	docker tag $(NAME):$(VERSION) docker.sunet.se/$(NAME):$(VERSION)
	docker tag $(NAME):$(VERSION) docker.sunet.se/$(NAME):$(BASE)
push:
	docker push docker.sunet.se/$(NAME):$(VERSION)
	docker push docker.sunet.se/$(NAME):$(BASE)
