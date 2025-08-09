.PHONY: build clean

BINARY_NAME=pprotein-mcp-server

build:
	go build -o $(BINARY_NAME) .

clean:
	go clean
	rm -f $(BINARY_NAME)
