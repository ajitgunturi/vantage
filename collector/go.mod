module github.com/ajitgunturi/vantage/collector

go 1.26

require (
	github.com/ajitgunturi/vantage/mq v0.0.0
	github.com/jackc/pgx/v5 v5.7.2
	google.golang.org/grpc v1.71.0
)

replace github.com/ajitgunturi/vantage/mq => ../mq
