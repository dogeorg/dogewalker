default: build/dogewalker

.PHONY: clean, test
clean:
	rm -rf ./build

build/dogewalker: clean
	mkdir -p build
	go build -o build/walker ./server/main.go

test:
	go test -v ./test/follower
