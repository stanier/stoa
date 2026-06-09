package agentproto

import "time"

// Project is the JSON wire representation of a project.
type Project struct {
	ID           string        `json:"id"`
	UserID       string        `json:"user_id,omitempty"`
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	Color        string        `json:"color"`
	Glyph        string        `json:"glyph"`
	Emoji        string        `json:"emoji"`
	Instructions string        `json:"instructions"`
	DefaultModel string        `json:"default_model"`
	DefaultTools []string      `json:"default_tools"`
	Files        []ProjectFile `json:"files"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// ProjectFile is a file metadata record attached to a project.
type ProjectFile struct {
	Name string `json:"name"`
	Size string `json:"size"`
}

// PatchProjectRequest is the body for PATCH /projects/:id.
// Only non-nil pointer fields are applied; slice fields replace the existing value when present.
type PatchProjectRequest struct {
	Name         *string       `json:"name,omitempty"`
	Description  *string       `json:"description,omitempty"`
	Color        *string       `json:"color,omitempty"`
	Glyph        *string       `json:"glyph,omitempty"`
	Emoji        *string       `json:"emoji,omitempty"`
	Instructions *string       `json:"instructions,omitempty"`
	DefaultModel *string       `json:"default_model,omitempty"`
	DefaultTools []string      `json:"default_tools,omitempty"`
	Files        []ProjectFile `json:"files,omitempty"`
}

// PatchSessionRequest is the body for PATCH /sessions/:id.
// project_id has no omitempty — JSON null means "remove from project".
type PatchSessionRequest struct {
	ProjectID *string `json:"project_id"`
}
