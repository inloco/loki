ifndef ACCOUNT_ID
$(error ACCOUNT_ID is not set)
endif

IMAGE_NAME ?= ecr-public/grafana/lambda-promtail
IMAGE_TAG ?= incognia-$(shell git describe --always --dirty --exclude '*')
IMAGE_REPO ?= ${ACCOUNT_ID}.dkr.ecr.us-east-1.amazonaws.com

docker-build:
	docker build -t $(IMAGE_NAME):$(IMAGE_TAG) -f tools/lambda-promtail/Dockerfile .
.PHONY: docker-build

docker-tag:
	docker tag $(IMAGE_NAME):$(IMAGE_TAG) $(IMAGE_REPO)/$(IMAGE_NAME):$(IMAGE_TAG)
	docker tag $(IMAGE_NAME):$(IMAGE_TAG) $(IMAGE_REPO)/$(IMAGE_NAME):$(IMAGE_TAG)
.PHONY: docker-tag

docker-push: docker-build docker-tag
	docker push $(IMAGE_REPO)/$(IMAGE_NAME):$(IMAGE_TAG)
.PHONY: docker-push

all: clean test build

GOTEST ?= go test

build:
	GOOS=linux CGO_ENABLED=0 go build -o ./main lambda-promtail/*.go

test:
	$(GOTEST) ./...

clean:
	rm -f main
