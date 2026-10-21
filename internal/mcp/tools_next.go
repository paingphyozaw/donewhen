package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (d *deps) registerNext(s *server.MCPServer) {
	s.AddTool(mcp.NewTool("next_issues",
		mcp.WithDescription("What to work on next: the Ready tickets with no open blocker, best first. The ticket at "+
			"the head of the longest chain of open work (the critical path) comes first, then the one that unlocks "+
			"most tickets, then priority, then the oldest. Each row has 'chain' (tickets in its longest chain, itself "+
			"included), 'chainKeys' and 'unlocks'. One workspace: pass 'workspace' when you reach several."),
		mcp.WithNumber("limit", mcp.Description("Max rows (default 10)")),
		wsArg(),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		wsID, err := d.scopeOne(ctx, req)
		if err != nil {
			return toolErr(err), nil
		}
		limit := req.GetInt("limit", 10)
		if limit <= 0 {
			limit = 10
		}
		items, err := d.store.NextIssues(ctx, wsID, limit)
		if err != nil {
			return toolErr(err), nil
		}
		return jsonResult(items)
	})
}
