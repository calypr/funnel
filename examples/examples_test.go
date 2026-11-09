package examples

import (
	"testing"

	"github.com/calypr/funnel/api/tes"
	taskpolicy "github.com/calypr/funnel/internal/task"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestExamplesAreValid(t *testing.T) {
	for en, tb := range Examples() {
		var task tes.Task
		err := protojson.Unmarshal([]byte(tb), &task)
		if err != nil {
			t.Fatal("unmarshal failed", en, err, tb)
		}
		if err := taskpolicy.Validate(&task); err != nil {
			t.Fatal("Invalid task message:", en, "\n", "error:", err)
		}
	}
}
