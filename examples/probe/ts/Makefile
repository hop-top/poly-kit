.PHONY: build run test deps

deps:
	npm install

build: deps
	npx tsc --noEmit

run: deps
	npx tsx probe.ts

test: deps
	npx vitest run
