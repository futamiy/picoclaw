package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// writeJSONFile marshals v as indented JSON and writes it to path.
func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// CompressScorecardMarkdown renders compress-cycle rows as a Markdown table
// matching the hermes-agent evals/compaction column layout.
//
// Columns: policy | recall | tokens before | tokens after | kept | freed | sec
func CompressScorecardMarkdown(rows []CompressCycleResult) string {
	sorted := append([]CompressCycleResult(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].RecallPct > sorted[j].RecallPct
	})

	var b strings.Builder
	b.WriteString("| policy | recall | tokens before | tokens after | kept | freed | sec | slight |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, r := range sorted {
		slight := "no"
		if r.Slight {
			slight = "yes"
		}
		fmt.Fprintf(&b, "| %s | %.1f%% | %d | %d | %.1f%% | %.1f%% | %.2f | %s |\n",
			r.Policy, r.RecallPct, r.TokensBefore, r.TokensAfter, r.KeptPct, r.FreedPct, r.CycleSeconds, slight)
	}
	return b.String()
}

// PrintCompressScorecard prints rows as a terminal table plus a Markdown block.
func PrintCompressScorecard(rows []CompressCycleResult) {
	sorted := append([]CompressCycleResult(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].RecallPct > sorted[j].RecallPct
	})

	fmt.Printf("\n--- Compression cycles ---\n")
	fmt.Printf("%-24s %-8s %-14s %-13s %-8s %-8s %-6s\n",
		"policy", "recall", "tokens before", "tokens after", "kept", "freed", "sec")
	fmt.Println(strings.Repeat("-", 90))
	for _, r := range sorted {
		fmt.Printf("%-24s %-8s %-14d %-13d %-8s %-8s %-6.2f\n",
			r.Policy,
			fmt.Sprintf("%.1f%%", r.RecallPct),
			r.TokensBefore,
			r.TokensAfter,
			fmt.Sprintf("%.1f%%", r.KeptPct),
			fmt.Sprintf("%.1f%%", r.FreedPct),
			r.CycleSeconds,
		)
	}
}

// SaveCompressMarkdown writes the Markdown scorecard to outDir/compress_scorecard.md.
func SaveCompressMarkdown(rows []CompressCycleResult, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create out dir: %w", err)
	}
	return os.WriteFile(
		outDir+"/compress_scorecard.md",
		[]byte(CompressScorecardMarkdown(rows)),
		0o644,
	)
}
