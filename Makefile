GITTAG=`git describe --tags`
VERSION=`git rev-parse --short HEAD`
# flags=-ldflags="-s -w -X main.gitVersion=${VERSION} -X main.gitTag=${GITTAG} -extldflags -static"
flags=-ldflags="-s -w -X main.gitVersion=${VERSION} -X main.gitTag=${GITTAG}"

all: build

vet:
	go vet .

build:
	go clean; rm -rf pkg; go build -o goimapsync ${flags}

build_debug:
	go clean; rm -rf pkg; go build -o goimapsync ${flags} -gcflags="-m -m"

build_amd64: build_linux

build_darwin:
	go clean; rm -rf pkg goimapsync; GOOS=darwin go build -o goimapsync ${flags}

build_linux:
	go clean; rm -rf pkg goimapsync; GOOS=linux go build -o goimapsync ${flags}

build_power8:
	go clean; rm -rf pkg goimapsync; GOARCH=ppc64le GOOS=linux go build -o goimapsync ${flags}

build_arm64:
	go clean; rm -rf pkg goimapsync; GOARCH=arm64 GOOS=linux go build -o goimapsync ${flags}

build_windows:
	go clean; rm -rf pkg goimapsync; GOARCH=amd64 GOOS=windows go build -o goimapsync ${flags}

install:
	go install

clean:
	go clean; rm -rf pkg

test : test1

test1:
	go test -v -bench=.
