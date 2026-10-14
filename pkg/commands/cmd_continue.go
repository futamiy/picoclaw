package commands

import (
	"context"
	"strings"
)

func continueCommand() Definition {
	return Definition{
		Name:        "continue",
		Description: "Resume the previous task (e.g. after a tool-iteration limit)",
		Usage:       "/continue",
		Handler: func(ctx context.Context, req Request, rt *Runtime) error {
			const nothingToContinueMsg = "Nothing to continue."

			if rt == nil || rt.Continue == nil {
				return req.Reply(unavailableMsg)
			}

			answer, err := rt.Continue(ctx)
			if err != nil {
				return req.Reply(err.Error())
			}
			if strings.TrimSpace(answer) == "" {
				return req.Reply(nothingToContinueMsg)
			}

			return req.Reply(answer)
		},
	}
}
