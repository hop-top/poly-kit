.PHONY: build test run-go run-ts run-py \
       build-go build-ts build-py \
       test-go test-ts test-py

build: build-go build-ts build-py
test: test-go test-ts test-py

build-go:
	$(MAKE) -C go build

build-ts:
	$(MAKE) -C ts build

build-py:
	$(MAKE) -C py build

test-go:
	$(MAKE) -C go test

test-ts:
	$(MAKE) -C ts test

test-py:
	$(MAKE) -C py test

run-go:
	cd go && make run

run-ts:
	cd ts && npx tsx probe.ts

run-py:
	cd py && python probe.py
