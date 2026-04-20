package knowledge

import (
	"context"
	"fmt"
	"os"
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
	repoID := repo.ID
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
	if err := WriteTree(cfg, repoID, scanResult.Tree); err != nil {
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

	for i := range l1Summaries {
		l1Summaries[i].RepoID = repoID
	}

	if err := WriteAgentSummaries(cfg, repoID, 1, l1Summaries); err != nil {
		return nil, fmt.Errorf("write l1: %w", err)
	}

	l1Tokens := 0
	for _, s := range l1Summaries {
		l1Tokens += s.Tokens
	}
	manifest.Levels = append(manifest.Levels, Level{
		RepoID:      repoID,
		Number:      1,
		AgentCount:  len(l1Summaries),
		TotalTokens: l1Tokens,
	})

	// Phases 4-6: compression → service docs → agent guide
	return finalizePipeline(ctx, client, cfg, absPath, repoID, manifest, l1Summaries, scanResult.Tree, progress)
}

// ReindexRepo performs true incremental re-indexing.
//
// It compares the current on-disk SHA hashes with the previously stored tree to
// classify every file as unchanged, modified, added, or deleted. Only the L1
// agent summaries whose file set intersects with the changed set are
// re-summarized; the rest are reused verbatim. New and modified files are
// redistributed into fresh agent assignments. Deleted files are dropped from
// their former agents. After the merged L1 is produced, the full compression
// hierarchy (L2 … Ln) and post-processing phases (service docs, agent guide)
// are rebuilt from scratch.
//
// The second return value is the number of files that triggered a change
// (adds + modifications + deletions). It is 0 when nothing changed.
func ReindexRepo(ctx context.Context, client CompletionClient, repoPath string, cfg Config, progress ProgressFunc) (*Manifest, int, error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, 0, fmt.Errorf("resolve path: %w", err)
	}

	repoID := RepoID(absPath)
	oldManifest, err := ReadManifest(cfg, repoID)
	if err != nil {
		// No prior index — full index.
		m, err := IndexRepo(ctx, client, repoPath, cfg, progress)
		if err != nil {
			return nil, 0, err
		}
		return m, m.Repo.FileCount, nil
	}

	// Phase 1: Scan current state.
	if progress != nil {
		progress("scanning", 0, 0)
	}
	scanResult, err := ScanRepo(absPath, cfg.ScanMode)
	if err != nil {
		return nil, 0, fmt.Errorf("scan: %w", err)
	}

	// Read old tree; fall back to full index if it is missing or corrupt.
	oldTree, err := ReadTree(cfg, repoID)
	if err != nil {
		m, err := IndexRepo(ctx, client, repoPath, cfg, progress)
		if err != nil {
			return nil, 0, err
		}
		return m, m.Repo.FileCount, nil
	}

	// Build lookup tables.
	oldHashes := make(map[string]string, len(oldTree))
	for _, n := range oldTree {
		oldHashes[n.Path] = n.Hash
	}
	newByPath := make(map[string]TreeNode, len(scanResult.Tree))
	for _, n := range scanResult.Tree {
		newByPath[n.Path] = n
	}

	// Classify every file.
	deleted := make(map[string]bool)
	modified := make(map[string]bool)
	added := make(map[string]bool)

	for path, oldHash := range oldHashes {
		if n, exists := newByPath[path]; !exists {
			deleted[path] = true
		} else if n.Hash != oldHash {
			modified[path] = true
		}
	}
	for path := range newByPath {
		if _, exists := oldHashes[path]; !exists {
			added[path] = true
		}
	}

	changedCount := len(deleted) + len(modified) + len(added)
	if changedCount == 0 {
		if progress != nil {
			progress("no-changes", 0, 0)
		}
		return &oldManifest, 0, nil
	}

	if progress != nil {
		progress("scanned", scanResult.FileCount, scanResult.FileCount)
	}

	// Read old L1 summaries; fall back to full index if unavailable.
	oldL1, err := ReadAgentSummaries(cfg, repoID, 1)
	if err != nil || len(oldL1) == 0 {
		m, err := IndexRepo(ctx, client, repoPath, cfg, progress)
		if err != nil {
			return nil, 0, err
		}
		return m, changedCount, nil
	}

	// Identify dirty agents: any agent whose file set overlaps with the
	// changed or deleted set must be re-summarized.
	dirtyAgentIdx := make(map[int]bool)
	for i, agent := range oldL1 {
		for _, fp := range agent.FilePaths {
			if deleted[fp] || modified[fp] {
				dirtyAgentIdx[i] = true
				break
			}
		}
	}

	// Separate clean agents from the dirty pool.
	var cleanSummaries []AgentSummary
	dirtyFileSet := make(map[string]bool)
	for i, agent := range oldL1 {
		if dirtyAgentIdx[i] {
			// Pool surviving (non-deleted) files for re-summarization.
			for _, fp := range agent.FilePaths {
				if !deleted[fp] {
					dirtyFileSet[fp] = true
				}
			}
		} else {
			cleanSummaries = append(cleanSummaries, agent)
		}
	}
	// New files always enter the dirty pool.
	for fp := range added {
		dirtyFileSet[fp] = true
	}

	// Re-summarize the dirty pool.
	var newSummaries []AgentSummary
	if len(dirtyFileSet) > 0 {
		var dirtyNodes []TreeNode
		for fp := range dirtyFileSet {
			if n, ok := newByPath[fp]; ok {
				dirtyNodes = append(dirtyNodes, n)
			}
		}
		if progress != nil {
			progress("distributing", 0, 0)
		}
		newAssignments := DistributeFiles(dirtyNodes, cfg)
		newSummaries, err = SummarizeAllAgents(ctx, client, cfg.GetIndexModel(), absPath, newAssignments, cfg, progress)
		if err != nil {
			return nil, 0, fmt.Errorf("incremental l1 summarize: %w", err)
		}
	}

	// Merge clean + new summaries and renumber contiguously.
	allL1 := make([]AgentSummary, 0, len(cleanSummaries)+len(newSummaries))
	allL1 = append(allL1, cleanSummaries...)
	allL1 = append(allL1, newSummaries...)
	if len(allL1) == 0 {
		return nil, 0, fmt.Errorf("no files remain after applying changes in %s", absPath)
	}
	for i := range allL1 {
		allL1[i].Index = i
		allL1[i].RepoID = repoID
	}

	// Persist the updated tree and merged L1.
	if err := WriteTree(cfg, repoID, scanResult.Tree); err != nil {
		return nil, 0, fmt.Errorf("write tree: %w", err)
	}
	if err := WriteAgentSummaries(cfg, repoID, 1, allL1); err != nil {
		return nil, 0, fmt.Errorf("write l1: %w", err)
	}

	// Rebuild manifest from the merged L1 (compression levels are always
	// regenerated by finalizePipeline).
	repo := oldManifest.Repo
	repo.FileCount = scanResult.FileCount
	repo.Status = "indexing"
	repo.UpdatedAt = time.Now().UTC()

	l1Tokens := 0
	for _, s := range allL1 {
		l1Tokens += s.Tokens
	}
	manifest := Manifest{
		Repo: repo,
		Levels: []Level{{
			RepoID:      repoID,
			Number:      1,
			AgentCount:  len(allL1),
			TotalTokens: l1Tokens,
		}},
	}

	// Phases 4-6: compression → service docs → agent guide.
	result, err := finalizePipeline(ctx, client, cfg, absPath, repoID, manifest, allL1, scanResult.Tree, progress)
	if err != nil {
		return nil, 0, err
	}
	return result, changedCount, nil
}

// finalizePipeline runs the compression loop (phase 4) and the post-processing
// phases (service docs, agent guide) given a ready L1 summary set. It writes
// the manifest and returns the final result.
//
// The incoming manifest must already contain the L1 Level entry and a Repo
// with Status = "indexing".
func finalizePipeline(
	ctx context.Context,
	client CompletionClient,
	cfg Config,
	absPath string,
	repoID string,
	manifest Manifest,
	l1Summaries []AgentSummary,
	tree []TreeNode,
	progress ProgressFunc,
) (*Manifest, error) {
	repo := manifest.Repo

	// Phase 4: Recursive compression.
	currentSummaries := l1Summaries
	currentLevel := 1
	const maxLevels = 10
	var cmap CompressionMap

	for currentLevel < maxLevels {
		totalTokens := 0
		for _, s := range currentSummaries {
			totalTokens += s.Tokens
		}

		if totalTokens <= cfg.TargetTokens {
			// Everything fits — produce the master context directly.
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
			if err := WriteMasterContext(cfg, repoID, currentLevel, masterContent); err != nil {
				return nil, fmt.Errorf("write master context: %w", err)
			}
			if err := WriteCompressionMap(cfg, repoID, currentLevel, cmap); err != nil {
				return nil, fmt.Errorf("write compression map: %w", err)
			}
			manifest.Levels = append(manifest.Levels, Level{
				RepoID:      repoID,
				Number:      currentLevel,
				AgentCount:  1,
				TotalTokens: estimateTokens(int64(len(masterContent))),
			})
			break
		}

		// Compress to next level.
		if progress != nil {
			progress(fmt.Sprintf("compressing-l%d", currentLevel+1), 0, len(currentSummaries))
		}
		compressed, isMaster, err := CompressLevel(ctx, client, cfg.GetIndexModel(), currentSummaries, currentLevel, cfg)
		if err != nil {
			return nil, fmt.Errorf("compress level %d: %w", currentLevel+1, err)
		}
		currentLevel++

		cmap.Levels = append(cmap.Levels, buildLevelIndex(currentLevel, compressed))

		if isMaster {
			masterContent := compressed[0].Summary
			if err := WriteMasterContext(cfg, repoID, currentLevel, masterContent); err != nil {
				return nil, fmt.Errorf("write master context: %w", err)
			}
			if err := WriteCompressionMap(cfg, repoID, currentLevel, cmap); err != nil {
				return nil, fmt.Errorf("write compression map: %w", err)
			}
			manifest.Levels = append(manifest.Levels, Level{
				RepoID:      repoID,
				Number:      currentLevel,
				AgentCount:  1,
				TotalTokens: estimateTokens(int64(len(masterContent))),
			})
			break
		}

		for i := range compressed {
			compressed[i].RepoID = repoID
		}
		if err := WriteAgentSummaries(cfg, repoID, currentLevel, compressed); err != nil {
			return nil, fmt.Errorf("write level %d: %w", currentLevel, err)
		}
		compressedTokens := 0
		for _, s := range compressed {
			compressedTokens += s.Tokens
		}
		manifest.Levels = append(manifest.Levels, Level{
			RepoID:      repoID,
			Number:      currentLevel,
			AgentCount:  len(compressed),
			TotalTokens: compressedTokens,
		})
		currentSummaries = compressed
		if progress != nil {
			progress(fmt.Sprintf("compressed-l%d", currentLevel), len(compressed), len(compressed))
		}
	}

	// Finalize repo metadata.
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

	// Phase 5: Generate service architecture documents (non-fatal).
	services := DetectServices(absPath, tree)
	if _, err := GenerateServiceDocs(ctx, client, absPath, cfg, manifest, progress); err != nil {
		fmt.Fprintf(os.Stderr, "service-docs warning: %v\n", err)
	}

	// Phase 6: Generate AI agent guide (non-fatal).
	if _, err := GenerateAgentGuide(ctx, client, cfg, manifest, services, progress); err != nil {
		fmt.Fprintf(os.Stderr, "agent-guide warning: %v\n", err)
	}

	if progress != nil {
		progress("done", 0, 0)
	}

	return &manifest, nil
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
