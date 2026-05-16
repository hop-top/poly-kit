.PHONY: build run test

build:
	cd ../../.. && go build -buildvcs=false -o examples/probe/go/probe ./examples/probe/go/

run: build
	./probe

test:
	cd ../../.. && go test ./examples/probe/go/... -count=1
