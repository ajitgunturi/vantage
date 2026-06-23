module github.com/ajitgunturi/vantage/streamer

go 1.26

// Dependencies (mq client lib, grpc, ...) are added via TDD as real code needs
// them. The replace directive returns when the first import of mq lands:
//   replace github.com/ajitgunturi/vantage/mq => ../mq
