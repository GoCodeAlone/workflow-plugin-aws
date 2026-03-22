// Command workflow-plugin-aws is a workflow engine external plugin that
// provides AWS infrastructure provisioning via the IaCProvider interface.
// It runs as a subprocess and communicates with the host workflow engine via
// the go-plugin protocol.
package main

import (
	"github.com/GoCodeAlone/workflow-plugin-aws/internal"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

func main() {
	sdk.Serve(internal.NewAWSPlugin())
}
