package commands

import (
	"context"
	"fmt"
)

func compactCommand() Definition {
	return Definition{
		Name:        "compact",
		Aliases:     []string{"compress"},
		Description: "Compress the current conversation context (summarize now)",
		Usage:       "/compact",
		Handler: func(ctx context.Context, req Request, rt *Runtime) error {
			if rt == nil || rt.Compact == nil {
				return req.Reply(unavailableMsg)
			}
			result, err := rt.Compact(ctx)
			if err != nil {
				return req.Reply("Failed to compact context: " + err.Error())
			}
			return req.Reply(formatCompactResult(result))
		},
	}
}

func formatCompactResult(r CompactResult) string {
	if r.BeforeMessages == r.AfterMessages {
		return fmt.Sprintf("Nothing to compact — context is already short (%d messages).", r.AfterMessages)
	}
	return fmt.Sprintf(
		"Context compacted: %d → %d messages (~%d → ~%d tokens).",
		r.BeforeMessages,
		r.AfterMessages,
		r.BeforeTokens,
		r.AfterTokens,
	)
}
