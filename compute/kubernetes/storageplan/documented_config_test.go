package storageplan

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/tes"
	"google.golang.org/protobuf/proto"
)

func TestDocumentedNativeStorageConfigurations(t *testing.T) {
	for _, file := range []string{"../../../website/content/docs/compute/kubernetes-storage.md", "../../../website/content/docs/storage/mounted-inputs.md"} {
		document, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		examples := regexp.MustCompile("(?s)```yaml\\n(.*?)\\n```").FindAllSubmatch(document, -1)
		checked := 0
		for i, example := range examples {
			raw := example[1]
			if !strings.Contains(string(raw), "Kubernetes:") {
				continue
			}
			checked++
			t.Run(fmt.Sprintf("%s/example-%d", file, i+1), func(t *testing.T) {
				parsed := config.EmptyConfig()
				if err := config.Parse(raw, parsed); err != nil {
					t.Fatal(err)
				}
				conf := config.DefaultConfig()
				proto.Merge(conf, parsed)
				plan, err := Resolve(&tes.Task{Id: "docs-task", Resources: &tes.Resources{DiskGb: 20}}, conf)
				if err != nil {
					t.Fatal(err)
				}
				if plan == nil || plan.WorkspaceVolume == "" {
					t.Fatal("example does not produce an explicit storage plan")
				}
			})
		}
		if checked == 0 {
			t.Fatalf("no executable storage examples in %s", file)
		}
	}
}
