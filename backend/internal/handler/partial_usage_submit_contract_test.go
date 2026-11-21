package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSemanticStreamFailoverSubmitsPartialUsageBeforeReturning(t *testing.T) {
	tests := []struct {
		name       string
		file       string
		submitCall string
		handleCall string
	}{
		{
			name:       "openai responses",
			file:       "openai_gateway_handler.go",
			submitCall: "submitResponsesUsage",
			handleCall: "handleFailoverExhausted",
		},
		{
			name:       "openai messages",
			file:       "openai_gateway_handler.go",
			submitCall: "submitMessagesUsage",
			handleCall: "handleAnthropicFailoverExhausted",
		},
		{
			name:       "gateway chat completions",
			file:       "gateway_handler_chat_completions.go",
			submitCall: "submitForwardUsage",
			handleCall: "handleCCFailoverExhausted",
		},
		{
			name:       "gateway responses",
			file:       "gateway_handler_responses.go",
			submitCall: "submitForwardUsage",
			handleCall: "handleResponsesFailoverExhausted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, filepath.Join(".", tt.file), nil, 0)
			require.NoError(t, err)

			found := false
			ast.Inspect(file, func(node ast.Node) bool {
				block, ok := node.(*ast.BlockStmt)
				if !ok {
					return true
				}
				for i := 1; i < len(block.List); i++ {
					if statementSubmitsUsage(block.List[i-1], tt.submitCall) && statementCalls(block.List[i], tt.handleCall) {
						found = true
						return false
					}
				}
				return true
			})

			require.True(t, found,
				"semantic-output failover must submit its partial usage immediately before the terminal handler")
		})
	}
}

// statementSubmitsUsage matches either a direct submit call or the retained-usage
// wrapper that bills the same closure once (issue #456): the partial usage of a
// semantic-output failover is still submitted immediately before the terminal
// handler, so it is never lost.
func statementSubmitsUsage(statement ast.Stmt, submitCall string) bool {
	if statementCalls(statement, submitCall) {
		return true
	}
	return statementCalls(statement, "settle") && statementCalls(statement, submitCall)
}

// statementCalls reports whether the statement mentions name, either as a call or as
// a value passed to another call (the retained-usage wrapper passes the submit
// closure as an argument).
func statementCalls(statement ast.Stmt, name string) bool {
	found := false
	ast.Inspect(statement, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.Ident:
			if n.Name == name {
				found = true
			}
		case *ast.CallExpr:
			switch fn := n.Fun.(type) {
			case *ast.Ident:
				if fn.Name == name {
					found = true
				}
			case *ast.SelectorExpr:
				if fn.Sel.Name == name {
					found = true
				}
			}
		}
		return !found
	})
	return found
}
