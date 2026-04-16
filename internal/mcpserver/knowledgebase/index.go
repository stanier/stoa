package knowledgebase

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// Chunk is a piece of a Markdown document with its embedding vector.
type Chunk struct {
	ID        string    `json:"id"`
	DocPath   string    `json:"doc_path"`  // relative to knowledge root
	Heading   string    `json:"heading"`   // breadcrumb, e.g. "Guide > Installation"
	Content   string    `json:"content"`
	Embedding []float32 `json:"embedding,omitempty"`
	FileHash  string    `json:"file_hash"` // hex sha256 of the source file at index time
}

type indexData struct {
	Version int      `json:"version"`
	Chunks  []*Chunk `json:"chunks"`
}

// Index holds all indexed chunks and persists them to disk.
type Index struct {
	mu   sync.RWMutex
	path string
	data indexData
}

func loadIndex(path string) (*Index, error) {
	idx := &Index{
		path: path,
		data: indexData{Version: 1},
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return idx, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open index: %w", err)
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&idx.data); err != nil {
		return nil, fmt.Errorf("corrupt index at %s: %w", path, err)
	}
	return idx, nil
}

// save atomically writes the index to disk.
func (idx *Index) save() error {
	idx.mu.RLock()
	data, err := json.MarshalIndent(idx.data, "", "  ")
	idx.mu.RUnlock()
	if err != nil {
		return err
	}
	tmp := idx.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, idx.path)
}

// chunks returns a snapshot of all chunks (caller must not modify).
func (idx *Index) chunks() []*Chunk {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make([]*Chunk, len(idx.data.Chunks))
	copy(out, idx.data.Chunks)
	return out
}

// fileHash returns the stored hash for all chunks belonging to docPath,
// or "" if the file is not indexed.
func (idx *Index) fileHash(docPath string) string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	for _, c := range idx.data.Chunks {
		if c.DocPath == docPath {
			return c.FileHash
		}
	}
	return ""
}

// replaceFile removes all chunks for docPath and appends new ones.
func (idx *Index) replaceFile(docPath string, chunks []*Chunk) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	out := idx.data.Chunks[:0]
	for _, c := range idx.data.Chunks {
		if c.DocPath != docPath {
			out = append(out, c)
		}
	}
	idx.data.Chunks = append(out, chunks...)
}

// removeFile removes all chunks for docPath.
func (idx *Index) removeFile(docPath string) {
	idx.replaceFile(docPath, nil)
}

// docPaths returns the set of doc paths currently in the index.
func (idx *Index) docPaths() map[string]struct{} {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	m := make(map[string]struct{}, len(idx.data.Chunks))
	for _, c := range idx.data.Chunks {
		m[c.DocPath] = struct{}{}
	}
	return m
}

// chunkID produces a stable ID from doc path, heading, and chunk offset within the file.
func chunkID(docPath, heading string, offset int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", docPath, heading, offset)))
	return fmt.Sprintf("%x", h[:8])
}
