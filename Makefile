BINARY = crush

.PHONY: build test clean install

build:
	go build -o $(BINARY) .

test:
	go test ./... -v

clean:
	rm -f $(BINARY)

install: build
	install -m 755 $(BINARY) /usr/local/bin/$(BINARY)
