package logger

import (
	"strings"
	"testing"

	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/sirupsen/logrus"
)

func TestFormatNilProtoField(t *testing.T) {
	if r := recover(); r != nil {
		t.Fatal("error")
	}
	var nt *tes.Task

	c := DebugConfig()
	tf := &textFormatter{
		c.TextFormat,
		jsonFormatter{
			conf: c.JsonFormat,
		},
	}

	entry := logrus.WithFields(logrus.Fields{
		"ns":        "TEST",
		"nil value": nt,
	})
	tf.Format(entry)
}

func TestFormatMultiLineString(t *testing.T) {
	c := DebugConfig()
	tf := &textFormatter{
		c.TextFormat,
		jsonFormatter{
			conf: c.JsonFormat,
		},
	}

	multiLineStr := "Line 1\nLine 2\nLine 3"

	entry := logrus.WithFields(logrus.Fields{
		"ns":          "TEST",
		"multi_line":  multiLineStr,
		"single_line": "simple",
	})

	result, err := tf.Format(entry)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	resultStr := string(result)

	// Check that multi-line string is properly formatted
	lines := strings.Split(resultStr, "\n")
	foundMultiLine := false
	for _, line := range lines {
		if strings.Contains(line, "multi_line") {
			foundMultiLine = true
			// Should contain proper indentation for continuation lines
			if strings.Contains(line, "Line 1") && strings.Contains(line, "Line 2") && strings.Contains(line, "Line 3") {
				// Check that continuation lines are properly indented
				parts := strings.Split(line, "Line 2")
				if len(parts) > 1 && !strings.HasPrefix(parts[1], " ") {
					t.Error("Multi-line continuation should be properly indented")
				}
			}
		}
	}

	if !foundMultiLine {
		t.Error("Multi-line field not found in formatted output")
	}
}

func TestFormatMultiLineWithLongKey(t *testing.T) {
	c := DebugConfig()
	tf := &textFormatter{
		c.TextFormat,
		jsonFormatter{
			conf: c.JsonFormat,
		},
	}

	multiLineStr := "Line 1\nLine 2"
	longKey := "very_long_key_name_that_exceeds_normal_padding"

	entry := logrus.WithFields(logrus.Fields{
		"ns":    "TEST",
		longKey: multiLineStr,
	})

	result, err := tf.Format(entry)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	resultStr := string(result)
	lines := strings.Split(resultStr, "\n")

	// Find the line with the long key
	var foundLine string
	for _, line := range lines {
		if strings.Contains(line, longKey) {
			foundLine = line
			break
		}
	}

	if foundLine == "" {
		t.Fatal("Line with long key not found")
	}

	// Should handle long keys properly without breaking formatting
	if !strings.Contains(foundLine, "Line 1") {
		t.Error("First line of multi-line content not found")
	}
}

func TestFormatComplexMultiLineContent(t *testing.T) {
	c := DebugConfig()
	tf := &textFormatter{
		c.TextFormat,
		jsonFormatter{
			conf: c.JsonFormat,
		},
	}

	complexContent := `This is a complex multi-line string:
- First item with some content
- Second item with more content
- Third item with even more content
End of content`

	entry := logrus.WithFields(logrus.Fields{
		"ns":      "TEST",
		"complex": complexContent,
		"simple":  "simple_value",
	})

	result, err := tf.Format(entry)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	resultStr := string(result)

	// Should preserve all content
	if !strings.Contains(resultStr, "First item") {
		t.Error("First line item missing from output")
	}
	if !strings.Contains(resultStr, "Second item") {
		t.Error("Second line item missing from output")
	}
	if !strings.Contains(resultStr, "Third item") {
		t.Error("Third line item missing from output")
	}

	// Should handle both multi-line and single-line fields in same log entry
	if !strings.Contains(resultStr, "simple_value") {
		t.Error("Single-line field missing from output")
	}
}

func TestFormatEmptyAndSingleLineStrings(t *testing.T) {
	c := DebugConfig()
	tf := &textFormatter{
		c.TextFormat,
		jsonFormatter{
			conf: c.JsonFormat,
		},
	}

	entry := logrus.WithFields(logrus.Fields{
		"ns":           "TEST",
		"empty_string": "",
		"single_char":  "a",
		"single_line":  "This is a single line",
	})

	result, err := tf.Format(entry)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	resultStr := string(result)

	// Should handle edge cases properly
	if !strings.Contains(resultStr, "empty_string") {
		t.Error("Empty string field missing from output")
	}
	if !strings.Contains(resultStr, "single_char") {
		t.Error("Single character field missing from output")
	}
	if !strings.Contains(resultStr, "single_line") {
		t.Error("Single line field missing from output")
	}
}
