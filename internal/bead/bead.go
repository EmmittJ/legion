// Package bead is Legion's work primitive: the Bead domain type and a
// wrapper around the bd CLI (embedded Dolt). All Legion components read
// and write work state exclusively through this package.
package bead

import (
	"strings"
	"time"
)

// Status values as reported by bd.
const (
	StatusOpen       = "open"
	StatusInProgress = "in_progress"
	StatusBlocked    = "blocked"
	StatusClosed     = "closed"
)

// Routing label prefixes. Routing is data on the bead:
// `vessel:<name>` selects the vessel image via the registry in
// .legion/config.toml; `persona:<name>` is passed through to the harness.
const (
	LabelVesselPrefix  = "vessel:"
	LabelPersonaPrefix = "persona:"
)

// Bead is a unit of work tracked in Beads.
type Bead struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	Priority    int       `json:"priority"`
	IssueType   string    `json:"issue_type"`
	Assignee    string    `json:"assignee"`
	Labels      []string  `json:"labels"`
	Acceptance  string    `json:"acceptance_criteria"`
	Notes       string    `json:"notes"`
	Parent      string    `json:"parent"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	ClosedAt    time.Time `json:"closed_at"`
	CloseReason string    `json:"close_reason"`
}

// Vessel returns the vessel name from the bead's routing labels,
// or "" when the bead does not select one (config default applies).
func (b *Bead) Vessel() string { return b.labelValue(LabelVesselPrefix) }

// Persona returns the persona name from the bead's routing labels,
// or "" when none is set.
func (b *Bead) Persona() string { return b.labelValue(LabelPersonaPrefix) }

func (b *Bead) labelValue(prefix string) string {
	for _, l := range b.Labels {
		if v, ok := strings.CutPrefix(l, prefix); ok {
			return v
		}
	}
	return ""
}

// VesselLabel builds a `vessel:<name>` routing label.
func VesselLabel(name string) string { return LabelVesselPrefix + name }

// PersonaLabel builds a `persona:<name>` routing label.
func PersonaLabel(name string) string { return LabelPersonaPrefix + name }

// Prompt renders the bead as a work prompt for a harness. Legion owns no
// persona formats: this is the only text the animus hands to the harness.
func (b *Bead) Prompt() string {
	var sb strings.Builder
	sb.WriteString(b.Title)
	sb.WriteString(" (")
	sb.WriteString(b.ID)
	sb.WriteString(")")
	if b.Description != "" {
		sb.WriteString("\n\n")
		sb.WriteString(b.Description)
	}
	if b.Acceptance != "" {
		sb.WriteString("\n\nAcceptance criteria:\n")
		sb.WriteString(b.Acceptance)
	}
	return sb.String()
}
