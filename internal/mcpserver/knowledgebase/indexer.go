package knowledgebase

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	// maxChunkChars is the soft character limit for a single chunk.
	// Chunks are split at paragraph boundaries when this is exceeded.
	maxChunkChars = 2000
)

type headingLevel struct {
	level int
	text  string
}

// Indexer walks a directory of Markdown files, chunks them, and embeds the chunks.
type Indexer struct {
	rootDir  string
	index    *Index
	embedder Embedder
}

func newIndexer(rootDir string, index *Index, embedder Embedder) *Indexer {
	return &Indexer{rootDir: rootDir, index: index, embedder: embedder}
}

// Sync walks rootDir and updates the index for any files that changed.
// Files that no longer exist are removed from the index.
func (ix *Indexer) Sync(ctx context.Context) error {
	// Collect all .md files on disk
	onDisk := make(map[string]struct{})
	err := filepath.WalkDir(ix.rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		rel, err := filepath.Rel(ix.rootDir, path)
		if err != nil {
			return err
		}
		onDisk[rel] = struct{}{}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", ix.rootDir, err)
	}

	// Remove stale entries
	for docPath := range ix.index.docPaths() {
		if _, exists := onDisk[docPath]; !exists {
			log.Printf("knowledgebase: removing stale %s", docPath)
			ix.index.removeFile(docPath)
		}
	}

	// Index new or changed files, collecting chunks that need embedding
	var toEmbed []*Chunk

	for rel := range onDisk {
		absPath := filepath.Join(ix.rootDir, rel)
		fileHash, err := hashFile(absPath)
		if err != nil {
			log.Printf("knowledgebase: hash %s: %v", rel, err)
			continue
		}
		if ix.index.fileHash(rel) == fileHash {
			continue // unchanged
		}
		log.Printf("knowledgebase: indexing %s", rel)
		chunks, err := chunkMarkdown(rel, absPath, fileHash)
		if err != nil {
			log.Printf("knowledgebase: chunk %s: %v", rel, err)
			continue
		}
		ix.index.replaceFile(rel, chunks)
		toEmbed = append(toEmbed, chunks...)
	}

	if len(toEmbed) == 0 {
		return nil
	}

	// Build text inputs: prefix each chunk with its heading breadcrumb so the
	// embedding captures document structure, not just raw content.
	texts := make([]string, len(toEmbed))
	for i, c := range toEmbed {
		if c.Heading != "" {
			texts[i] = c.Heading + "\n\n" + c.Content
		} else {
			texts[i] = c.Content
		}
	}

	log.Printf("knowledgebase: embedding %d chunks", len(texts))
	vectors, err := ix.embedder.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("embed chunks: %w", err)
	}
	for i, v := range vectors {
		toEmbed[i].Embedding = v
	}

	return nil
}

// chunkMarkdown reads an .md file and splits it into heading-bounded chunks.
func chunkMarkdown(relPath, absPath, fileHash string) ([]*Chunk, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		chunks       []*Chunk
		headingStack []headingLevel
		buf          strings.Builder
		chunkOffset  int
	)

	flush := func() {
		content := strings.TrimSpace(buf.String())
		if content == "" {
			return
		}
		breadcrumb := buildBreadcrumb(headingStack)
		c := &Chunk{
			DocPath:  relPath,
			Heading:  breadcrumb,
			Content:  content,
			FileHash: fileHash,
		}
		c.ID = chunkID(relPath, breadcrumb, chunkOffset)
		chunks = append(chunks, c)
		chunkOffset++
		buf.Reset()
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()

		// Detect ATX headings: # H1, ## H2, ### H3, ...
		level, text, ok := parseHeading(line)
		if ok {
			flush()
			// Pop headings of same or lower depth
			for len(headingStack) > 0 && headingStack[len(headingStack)-1].level >= level {
				headingStack = headingStack[:len(headingStack)-1]
			}
			headingStack = append(headingStack, headingLevel{level: level, text: text})
			// Include the heading line in the chunk so it's searchable as text
			buf.WriteString(line)
			buf.WriteByte('\n')
			continue
		}

		buf.WriteString(line)
		buf.WriteByte('\n')

		// Split on paragraph boundary if chunk exceeds the size limit
		if buf.Len() >= maxChunkChars && strings.HasSuffix(strings.TrimRight(buf.String(), " \t"), "\n\n") {
			flush()
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	flush()

	return chunks, nil
}

// parseHeading detects ATX-style Markdown headings (# through ######).
func parseHeading(line string) (int, string, bool) {
	if len(line) == 0 || line[0] != '#' {
		return 0, "", false
	}
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level > 6 {
		return 0, "", false
	}
	rest := line[level:]
	if len(rest) > 0 && rest[0] != ' ' && rest[0] != '\t' {
		return 0, "", false
	}
	text := strings.TrimFunc(rest, unicode.IsSpace)
	return level, text, true
}

// buildBreadcrumb joins heading texts with " > ".
func buildBreadcrumb(stack []headingLevel) string {
	parts := make([]string, len(stack))
	for i, h := range stack {
		parts[i] = h.text
	}
	return strings.Join(parts, " > ")
}

// hashFile returns the hex SHA-256 of the file's contents.
func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:]), nil
}
