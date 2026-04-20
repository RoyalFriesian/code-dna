package knowledge

import (
	"path/filepath"
	"sort"
)

// DistributeFiles groups scanned files into agent assignments for L1 summarization.
// Files are grouped by directory proximity, then balanced by token count.
func DistributeFiles(files []TreeNode, cfg Config) []AgentAssignment {
	if len(files) == 0 {
		return nil
	}

	maxFiles := cfg.AgentFileLimit
	if maxFiles <= 0 {
		maxFiles = 5
	}
	maxTokens := cfg.AgentTokenBudget
	if maxTokens <= 0 {
		maxTokens = 50000
	}

	// Sort files by directory then name for locality grouping
	sorted := make([]TreeNode, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool {
		di := filepath.Dir(sorted[i].Path)
		dj := filepath.Dir(sorted[j].Path)
		if di != dj {
			return di < dj
		}
		return sorted[i].Path < sorted[j].Path
	})

	var assignments []AgentAssignment
	var current AgentAssignment
	currentTokens := 0

	flush := func() {
		if len(current.FilePaths) > 0 {
			current.Index = len(assignments)
			assignments = append(assignments, current)
		}
		current = AgentAssignment{}
		currentTokens = 0
	}

	for _, f := range sorted {
		// Large files get their own agent
		if f.Tokens > maxTokens {
			flush()
			assignments = append(assignments, AgentAssignment{
				Index:      len(assignments),
				FilePaths:  []string{f.Path},
				TotalBytes: f.Size,
			})
			continue
		}

		// Check if adding this file would exceed limits
		wouldExceedFiles := len(current.FilePaths) >= maxFiles
		wouldExceedTokens := currentTokens+f.Tokens > maxTokens

		if wouldExceedFiles || wouldExceedTokens {
			flush()
		}

		current.FilePaths = append(current.FilePaths, f.Path)
		current.TotalBytes += f.Size
		currentTokens += f.Tokens
	}

	flush()

	return assignments
}
