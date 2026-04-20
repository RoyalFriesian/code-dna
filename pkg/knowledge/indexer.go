package knowledge

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"time"
)

// IndexRepo runs the full indexing pipeline: scan → distribute → summarize → compress.
func IndexRepo(ctx context.Context, client CompletionClient, repoPath string, cfg Config, progress ProgressFunc) (*Manifest, error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	repo := NewRepo(absPath, cfg.GetIndexModel())
	manifest := Manifest{Repo: repo}

	// Phase 1: Scan
	if progress != nil {
		progress("scanning", 0, 0)
	}
	scanResult, err := ScanRepo(absPath, cfg.ScanMode)
	if err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	if scanResult.FileCount == 0 {
		return nil, fmt.Errorf("no indexable files found in %s", absPath)
	}

	repo.FileCount = scanResult.FileCount
	repo.Status = "indexing"
	manifest.Repo = repo

	// Write L0 tree
	if err := WriteTree(cfg, repo.ID, scanResult.Tree); err != nil {
		return nil, fmt.Errorf("write tree: %w", err)
	}

	if progress != nil {
		progress("scanned", scanResult.FileCount, scanResult.FileCount)
	}

	// Phase 2: Distribute
	assignments := DistributeFiles(scanResult.Tree, cfg)
	if len(assignments) == 0 {
		return nil, fmt.Errorf("distribution produced no agent assignments")
	}

	if progress != nil {
		progress("distributing", len(assignments), len(assignments))
	}

	// Phase 3: L1 Summarization
	l1Summaries, err := SummarizeAllAgents(ctx, client, cfg.GetIndexModel(), absPath, assignments, cfg, progress)
	if err != nil {
		repo.Status = "failed"
		manifest.Repo = repo
		_ = WriteManifest(cfg, manifest)
		return nil, fmt.Errorf("l1 summarize: %w", err)
	}

	// Set repo ID on all summaries
	for i := range l1Summaries {
		l1Summaries[i].RepoID = repo.ID
	}

	// Write L1 summaries
	if err := WriteAgentSummaries(cfg, repo.ID, 1, l1Summaries); err != nil {
		return nil, fmt.Errorf("write l1: %w", err)
	}

	l1Tokens := 0
	for _, s := range l1Summaries {
		l1Tokens += s.Tokens
	}
	manifest.Levels = append(manifest.Levels, Level{
		RepoID:      repo.ID,
		Number:      1,
		AgentCount:  len(l1Summaries),
		TotalTokens: l1Tokens,
	})

	// Phase 4: Recursive compression
	currentSummaries := l1Summaries
	currentLevel := 1
	const maxLevels = 10    // safety bound
	var cmap CompressionMap // accumulated across compression iterations

	for currentLevel < maxLevels {
		totalTokens := 0
		for _, s := range currentSummaries {
			totalTokens += s.Tokens
		}

		if totalTokens <= cfg.TargetTokens {
			// Produce master context
			if progress != nil {
				progress("master-context", 0, 0)
			}
			compressed, _, err := CompressLevel(ctx, client, cfg.GetIndexModel(), currentSummaries, currentLevel, cfg)
			if err != nil {
				return nil, fmt.Errorf("master context: %w", err)
			}
			currentLevel++
			masterContent := compressed[0].Summary

			cmap.Levels = append(cmap.Levels, buildLevelIndex(currentLevel, compressed))

			if err := WriteMasterContext(cfg, repo.ID, currentLevel, masterContent); err != nil {
				return nil, fmt.Errorf("write master context: %w", err)
			}

			if err := WriteCompressionMap(cfg, repo.ID, currentLevel, cmap); err != nil {
				return nil, fmt.Errorf("write compression map: %w", err)
			}

			manifest.Levels = append(manifest.Levels, Level{
				RepoID:      repo.ID,
				Number:      currentLevel,
				AgentCount:  1,
				TotalTokens: estimateTokens(int64(len(masterContent))),
			})
			break
		}

		// Compress to next level
		if progress != nil {
			progress(fmt.Sprintf("compressing-l%d", currentLevel+1), 0, len(currentSummaries))
		}

		compressed, isMaster, err := CompressLevel(ctx, client, cfg.GetIndexModel(), currentSummaries, currentLevel, cfg)
		if err != nil {
			return nil, fmt.Errorf("compress level %d: %w", currentLevel+1, err)
		}

		currentLevel++

		if isMaster {
			masterContent := compressed[0].Summary

			cmap.Levels = append(cmap.Levels, buildLevelIndex(currentLevel, compressed))

			if err := WriteMasterContext(cfg, repo.ID, currentLevel, masterContent); err != nil {
				return nil, fmt.Errorf("write master context: %w", err)
			}
			if err := WriteCompressionMap(cfg, repo.ID, currentLevel, cmap); err != nil {
				return nil, fmt.Errorf("write compression map: %w", err)
			}

			compressedTokens := estimateTokens(int64(len(masterContent)))
			manifest.Levels = append(manifest.Levels, Level{
				RepoID:      repo.ID,
				Number:      currentLevel,
				AgentCount:  1,
				TotalTokens: compressedTokens,
			})
			break
		}

		// Record compression routing for this level
		cmap.Levels = append(cmap.Levels, buildLevelIndex(currentLevel, compressed))

		// Set repo ID on compressed summaries
		for i := range compressed {
			compressed[i].RepoID = repo.ID
		}

		if err := WriteAgentSummaries(cfg, repo.ID, currentLevel, compressed); err != nil {
			return nil, fmt.Errorf("write level %d: %w", currentLevel, err)
		}

		compressedTokens := 0
		for _, s := range compressed {
			compressedTokens += s.Tokens
		}
		manifest.Levels = append(manifest.Levels, Level{
			RepoID:      repo.ID,
			Number:      currentLevel,
			AgentCount:  len(compressed),
			TotalTokens: compressedTokens,
		})

		currentSummaries = compressed

		if progress != nil {
			progress(fmt.Sprintf("compressed-l%d", currentLevel), len(compressed), len(compressed))
		}
	}

	// Finalize
	repo.LevelsCount = currentLevel
	repo.Status = "ready"
	repo.UpdatedAt = time.Now().UTC()

	totalTokens := 0
	for _, l := range manifest.Levels {
		totalTokens += l.TotalTokens
	}
	repo.TotalTokens = totalTokens
	manifest.Repo = repo

	if err := WriteManifest(cfg, manifest); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}

	if progress != nil {
		progress("done", 0, 0)
	}

	return &manifest, nil
}

// ReindexRepo performs incremental re-indexing by comparing file hashes.
func ReindexRepo(ctx context.Context, client CompletionClient, repoPath string, cfg Config, progress ProgressFunc) (*Manifest, int, error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, 0, fmt.Errorf("resolve path: %w", err)
	}

	repoID := RepoID(absPath)
	oldManifest, err := ReadManifest(cfg, repoID)
	if err != nil {
		// No existing index — do a full index
		m, err := IndexRepo(ctx, client, repoPath, cfg, progress)
		if err != nil {
			return nil, 0, err
		}
		return m, m.Repo.FileCount, nil
	}

	// Scan current state
	scanResult, err := ScanRepo(absPath, cfg.ScanMode)
	if err != nil {
		return nil, 0, fmt.Errorf("scan: %w", err)
	}

	// Read old tree and build hash map
	oldTree, err := ReadTree(cfg, repoID)
	if err != nil {
		// Corrupted — full reindex
		m, err := IndexRepo(ctx, client, repoPath, cfg, progress)
		if err != nil {
			return nil, 0, err
		}
		return m, m.Repo.FileCount, nil
	}

	oldHashes := make(map[string]string)
	for _, n := range oldTree {
		oldHashes[n.Path] = n.Hash
	}

	// Find changed files
	var changedFiles int
	for _, n := range scanResult.Tree {
		if oldHash, exists := oldHashes[n.Path]; !exists || oldHash != n.Hash {
			changedFiles++
		}
	}

	// Check for deleted files
	newPaths := make(map[string]bool)
	for _, n := range scanResult.Tree {
		newPaths[n.Path] = true
	}
	for _, n := range oldTree {
		if !newPaths[n.Path] {
			changedFiles++
		}
	}

	if changedFiles == 0 {
		if progress != nil {
			progress("no-changes", 0, 0)
		}
		return &oldManifest, 0, nil
	}

	// For now, if there are changes, do a full reindex.
	// A more sophisticated version would only re-summarize affected L1 agents
	// and cascade upward.
	_ = oldManifest
	m, err := IndexRepo(ctx, client, repoPath, cfg, progress)
	if err != nil {
		return nil, 0, err
	}
	return m, changedFiles, nil
}

// buildLevelIndex creates a LevelIndex from compressed summaries, mapping each
// parent agent index to the child agent indices it was built from (GroupIDs).
func buildLevelIndex(level int, summaries []AgentSummary) LevelIndex {
	entries := make(map[string][]int, len(summaries))
	for _, s := range summaries {
		key := strconv.Itoa(s.Index)
		entries[key] = s.GroupIDs
	}
	return LevelIndex{Level: level, Entries: entries}
}
