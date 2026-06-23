module github.com/ajitgunturi/vantage/streamer

go 1.26

require (
	github.com/ajitgunturi/vantage/mq v0.0.0
	google.golang.org/grpc v1.71.0
)

// Local multi-module wiring: build standalone (Docker) without go.work.
replace github.com/ajitgunturi/vantage/mq => ../mq
