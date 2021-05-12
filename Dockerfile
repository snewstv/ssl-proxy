FROM golang:1.15-alpine
WORKDIR /go/src/github.com/snewstv/ssl-proxy
RUN apk add --no-cache make git zip
RUN go get -u github.com/golang/dep/cmd/dep
COPY . .
RUN make 
