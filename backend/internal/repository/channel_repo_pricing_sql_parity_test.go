//go:build unit

package repository

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// A mismatch between INSERT target columns and supplied placeholders is a
// runtime error that no mock test catches: sqlmock matches the SQL text, so a
// statement with 17 columns and $1..$16 shipped to production and every channel
// pricing save failed with "INSERT has more target columns than expressions".
// This guard reads the real statements and compares the two counts.
func TestChannelModelPricingSQLColumnPlaceholderParity(t *testing.T) {
	source, err := os.ReadFile("channel_repo_pricing.go")
	if err != nil {
		t.Fatalf("read repository source: %v", err)
	}
	body := string(source)

	insert := regexp.MustCompile(`(?s)INSERT INTO channel_model_pricing \((.*?)\)\s*VALUES \((.*?)\)`)
	match := insert.FindStringSubmatch(body)
	if match == nil {
		t.Fatal("INSERT statement for channel_model_pricing not found")
	}

	columns := strings.Count(match[1], ",") + 1
	placeholders := strings.Count(match[2], "$")

	if columns != placeholders {
		t.Fatalf("INSERT has %d target columns but %d placeholders", columns, placeholders)
	}

	update := regexp.MustCompile(`(?s)UPDATE channel_model_pricing\s*SET (.*?)\s*WHERE id = \$(\d+)`)
	updateMatch := update.FindStringSubmatch(body)
	if updateMatch == nil {
		t.Fatal("UPDATE statement for channel_model_pricing not found")
	}
	// The WHERE clause placeholder is bound last, after every SET column.
	setColumns := strings.Count(updateMatch[1], ",") + 1
	updatePlaceholders := strings.Count(updateMatch[1], "$") + 1

	if setColumns != updatePlaceholders {
		t.Fatalf("UPDATE sets %d columns but binds %d placeholders (including id)", setColumns, updatePlaceholders)
	}
}
