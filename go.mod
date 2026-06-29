// Single Go module for the vantage monorepo. The four microservices are
// independent by directory + Dockerfile (cmd/*), sharing only the protocol
// contracts and DB/model code in pkg/*. Change the module path here if the
// repo moves; it is referenced by all internal imports.
module github.com/ajitg/vantage

go 1.26

require (
	github.com/stretchr/testify v1.11.1
	golang.org/x/sync v0.21.0
	google.golang.org/grpc v1.81.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
