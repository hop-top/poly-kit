// Command cmdsurface-faas-lambda demonstrates the Lambda adapter
// pattern: the bridge is built once at module scope and the handler
// closure is exposed via aws-lambda-go.
//
// Two env vars configure the deployable shape so adopters can deploy
// multiple Lambda functions — one per leaf — from the same image:
//
//	CMDSURF_EVENT  one of apigw_v2 | apigw_v1 | eventbridge | sqs | direct
//	               (default: apigw_v2)
//	CMDSURF_LEAF   leaf path, space-separated (default: ping)
//	               e.g. "echo", "ping", "stamp"
//
// Example zip-bundle build (the canonical Lambda deploy path):
//
//	GOOS=linux GOARCH=arm64 go build \
//	    -tags lambda.norpc \
//	    -trimpath -ldflags='-s -w' \
//	    -o bootstrap ./examples/cmdsurface-faas/cmd/lambda
//	zip function.zip bootstrap
//	aws lambda update-function-code --function-name cmdsurface-ping \
//	    --zip-file fileb://function.zip
//
// See Dockerfile.example for the container-image deploy path.
package main

import (
	"log"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/lambda"
	"hop.top/kit/examples/cmdsurface-faas/shared"
	"hop.top/kit/go/transport/cmdsurface"
)

// bridge is constructed once at module init. Lambda containers stay
// warm across invocations; reusing the bridge avoids paying the
// cobra-tree-build + leaf-discovery cost on every event.
var bridge = shared.BuildBridge()

func main() {
	eventType := cmdsurface.LambdaEventType(os.Getenv("CMDSURF_EVENT"))
	if eventType == "" {
		eventType = cmdsurface.EventAPIGatewayV2
	}
	leaf := os.Getenv("CMDSURF_LEAF")
	if leaf == "" {
		leaf = "ping"
	}

	cfg := cmdsurface.LambdaConfig{
		Event: eventType,
		Mapping: cmdsurface.LambdaMapping{
			Path: pathFromEnv(leaf),
			FlagMap: map[string]string{
				"message": `{{ .body.message }}`,
				"who":     `{{ .body.who }}`,
			},
		},
	}

	// EventDirect ignores Mapping (the event JSON is the Invocation
	// literal). The other event types validate against Mapping.Path,
	// so we leave the rest of cfg intact.
	h, err := cmdsurface.LambdaHandler(bridge, cfg)
	if err != nil {
		log.Fatalf("cmdsurface.LambdaHandler: %v", err)
	}
	lambda.Start(h)
}

// pathFromEnv splits a whitespace-separated CMDSURF_LEAF value into
// the path slice the bridge expects. "echo" → ["echo"]; "widget add"
// → ["widget","add"].
func pathFromEnv(s string) []string {
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return []string{"ping"}
	}
	return parts
}
