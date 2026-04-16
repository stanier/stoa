package permissions

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// FileStore implements Store using YAML files
type FileStore struct {
	path string
}

// NewFileStore creates a new file-based store
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

// Save writes the storage data to disk atomically
func (s *FileStore) Save(data *StorageData) error {
	// Ensure directory exists
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Marshal to YAML
	yamlData, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	// Write atomically using temp file + rename
	tempPath := s.path + ".tmp"
	if err := os.WriteFile(tempPath, yamlData, 0600); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tempPath, s.path); err != nil {
		os.Remove(tempPath) // Clean up temp file
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// Load reads the storage data from disk
func (s *FileStore) Load() (*StorageData, error) {
	// Check if file exists
	if _, err := os.Stat(s.path); os.IsNotExist(err) {
		// Return empty data if file doesn't exist
		return &StorageData{
			Policies: []PermissionPolicy{},
			Grants:   []PermissionGrant{},
		}, nil
	}

	// Read file
	yamlData, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Unmarshal YAML
	var data StorageData
	if err := yaml.Unmarshal(yamlData, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %w", err)
	}

	// Initialize empty slices if nil
	if data.Policies == nil {
		data.Policies = []PermissionPolicy{}
	}
	if data.Grants == nil {
		data.Grants = []PermissionGrant{}
	}

	return &data, nil
}
