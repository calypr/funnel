package examples

import (
	"testing"

	"github.com/ohsu-comp-bio/funnel/tes"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestExamplesAreValid(t *testing.T) {
	for en, tb := range Examples() {
		var task tes.Task
		err := protojson.Unmarshal([]byte(tb), &task)
		if err != nil {
			t.Fatal("unmarshal failed", en, err)
		}
		if err := tes.Validate(&task); err != nil {
			t.Fatal("Invalid task message:", en, "\n", "error:", err)
		}
	}
}
