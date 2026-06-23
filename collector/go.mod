module github.com/ajitgunturi/vantage/collector

go 1.26

// Dependencies (mq client lib, pgx, grpc, ...) are added via TDD as real code
// needs them. The replace directive returns when the first import of mq lands:
//   replace github.com/ajitgunturi/vantage/mq => ../mq
