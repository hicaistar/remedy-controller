FROM golang:1.10.3 as builder

ENV GOPATH /go

COPY . $GOPATH/src/remedy-controller/

WORKDIR $GOPATH/src/remedy-controller/

RUN CGO_ENABLED=0 GOOS=linux \
	go build -a -ldflags '-extldflags "-static"' -o remedy-controller ./cmd && \
	cp remedy-controller /bin

# The container where eviction-agent will be run
FROM scratch

COPY --from=builder /bin/remedy-controller /

ENTRYPOINT ["/remedy-controller"]