IMAGE_URL ?= 248174752766.dkr.ecr.us-west-1.amazonaws.com/shorten

docker: .docker

.docker: Dockerfile shorten
	@ set -e; \
	eval "$$(aws ecr get-login)" && \
	docker build -t ${IMAGE_URL} . && \
	docker push ${IMAGE_URL} && \
	touch .docker

shorten: main.go
	CGO_ENABLED=0 go build -o shorten .
