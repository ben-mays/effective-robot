FROM golang:1.11

WORKDIR /go/src/github.com/ben-mays/effective-robot/
COPY . .

RUN make build

CMD ["bins/effective-robot"]