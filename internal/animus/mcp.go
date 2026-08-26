package animus

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/EmmittJ/legion/internal/bead"
	"github.com/EmmittJ/legion/internal/telemetry"
)

// mcpBeadAPI is the slice of the bead layer exposed over MCP.
// *bead.Client satisfies it; tests fake it.
type mcpBeadAPI interface {
	Get(ctx context.Context, id string) (*bead.Bead, error)
	Trace(ctx context.Context, id, text string) error
	Create(ctx context.Context, title string, opts bead.CreateOpts) (*bead.Bead, error)
	Children(ctx context.Context, id string) ([]bead.Bead, error)
}

// BeadView is the bead shape returned to the working model.
type BeadView struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status"`
	Labels      []string `json:"labels,omitempty"`
	Acceptance  string   `json:"acceptance_criteria,omitempty"`
}

func view(b *bead.Bead) BeadView {
	return BeadView{
		ID:          b.ID,
		Title:       b.Title,
		Description: b.Description,
		Status:      b.Status,
		Labels:      b.Labels,
		Acceptance:  b.Acceptance,
	}
}

// TraceArgs are the arguments for bead_trace.
type TraceArgs struct {
	Text string `json:"text" jsonschema:"progress note to append to the bead"`
}

// DiscoverArgs are the arguments for bead_discover.
type DiscoverArgs struct {
	Title       string `json:"title" jsonschema:"title of the discovered work"`
	Description string `json:"description,omitempty" jsonschema:"details of the discovered work"`
	Priority    int    `json:"priority,omitempty" jsonschema:"priority 0 (critical) to 4 (backlog); default 2"`
}

// DiscoverResult reports the filed bead.
type DiscoverResult struct {
	ID string `json:"id"`
}

// ChildrenResult lists related child beads.
type ChildrenResult struct {
	Beads []BeadView `json:"beads"`
}

// MCPServer builds the animus's MCP server: Legion's primitives, scoped
// to one bead. The working model sees these tools instead of the bd CLI
// or any credentials; every call lands in Dolt history. See ADR-0005.
func MCPServer(beads mcpBeadAPI, beadID string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "legion", Version: "1.0.0"}, nil)

	span := func(ctx context.Context, tool string) (context.Context, trace.Span) {
		return tracer().Start(ctx, "mcp."+tool, trace.WithAttributes(
			attribute.String(telemetry.AttrBeadID, beadID),
		))
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "bead_get",
		Description: "Get the bead (unit of work) this session is working on: title, description, acceptance criteria, status.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, BeadView, error) {
		ctx, sp := span(ctx, "bead_get")
		defer sp.End()
		b, err := beads.Get(ctx, beadID)
		if err != nil {
			return nil, BeadView{}, err
		}
		return nil, view(b), nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "bead_trace",
		Description: "Append a progress note to this session's bead. Use it to record decisions, findings, and status.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args TraceArgs) (*mcp.CallToolResult, struct{}, error) {
		ctx, sp := span(ctx, "bead_trace")
		defer sp.End()
		if args.Text == "" {
			return nil, struct{}{}, fmt.Errorf("text is required")
		}
		return nil, struct{}{}, beads.Trace(ctx, beadID, args.Text)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "bead_discover",
		Description: "File a new bead for work discovered while working this one. It is linked discovered-from this bead and worked later.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args DiscoverArgs) (*mcp.CallToolResult, DiscoverResult, error) {
		ctx, sp := span(ctx, "bead_discover")
		defer sp.End()
		if args.Title == "" {
			return nil, DiscoverResult{}, fmt.Errorf("title is required")
		}
		priority := args.Priority
		if priority == 0 {
			priority = 2
		}
		nb, err := beads.Create(ctx, args.Title, bead.CreateOpts{
			Description:    args.Description,
			IssueType:      "task",
			Priority:       priority,
			DiscoveredFrom: beadID,
		})
		if err != nil {
			return nil, DiscoverResult{}, err
		}
		return nil, DiscoverResult{ID: nb.ID}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "bead_children",
		Description: "List child beads of this session's bead (subtasks and discovered work).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ChildrenResult, error) {
		ctx, sp := span(ctx, "bead_children")
		defer sp.End()
		kids, err := beads.Children(ctx, beadID)
		if err != nil {
			return nil, ChildrenResult{}, err
		}
		res := ChildrenResult{Beads: make([]BeadView, 0, len(kids))}
		for i := range kids {
			res.Beads = append(res.Beads, view(&kids[i]))
		}
		return nil, res, nil
	})

	return s
}
