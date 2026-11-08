package mongodb

import (
	"fmt"
	"testing"

	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/tests/mongotest"
)

func TestListTasksNamePrefixTreatsRegexPunctuationLiterally(t *testing.T) {
	db := newMongoTestDB(t, mongotest.Start(t), "name_prefix_literal")
	type taskName struct {
		id   string
		name string
	}
	cases := []struct {
		name   string
		prefix string
		tasks  []taskName
		want   []string
	}{
		{name: "dot", prefix: "qa-dot.", tasks: []taskName{{"dot-literal", "qa-dot.value"}, {"dot-regex-match", "qa-dotXvalue"}}, want: []string{"dot-literal"}},
		{name: "plus", prefix: "qa-plus+", tasks: []taskName{{"plus-literal", "qa-plus+value"}, {"plus-regex-match", "qa-plussvalue"}}, want: []string{"plus-literal"}},
		{name: "open bracket", prefix: "qa-open[", tasks: []taskName{{"bracket-literal", "qa-open[value"}, {"bracket-regex-match", "qa-openxvalue"}}, want: []string{"bracket-literal"}},
		{name: "backslash", prefix: `qa-backslash\`, tasks: []taskName{{"backslash-literal", `qa-backslash\value`}, {"backslash-regex-match", "qa-backslashvalue"}}, want: []string{"backslash-literal"}},
		{name: "caret", prefix: "qa-caret^", tasks: []taskName{{"caret-literal", "qa-caret^value"}, {"caret-regex-match", "qa-caretvalue"}}, want: []string{"caret-literal"}},
		{name: "dollar", prefix: "qa-dollar$", tasks: []taskName{{"dollar-literal", "qa-dollar$value"}, {"dollar-regex-match", "qa-dollar"}}, want: []string{"dollar-literal"}},
		{name: "ordinary prefix with pagination", prefix: "ordinary-", tasks: []taskName{{"ordinary-one", "ordinary-one"}, {"ordinary-two", "ordinary-two"}, {"ordinary-near-miss", "ordinaryx"}}, want: []string{"ordinary-two", "ordinary-one"}},
	}

	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for i, task := range tc.tasks {
				creationTime := fmt.Sprintf("2026-01-%02dT00:00:00Z", index*3+i+1)
				if _, err := db.tasks().InsertOne(t.Context(), taskRecord{Task: &tes.Task{Id: task.id, Name: task.name, CreationTime: creationTime}}); err != nil {
					t.Fatalf("insert task %q: %v", task.id, err)
				}
			}

			var got []string
			token := ""
			for {
				page, err := db.ListTasks(t.Context(), &tes.ListTasksRequest{
					NamePrefix: tc.prefix,
					View:       tes.View_BASIC.String(),
					PageSize:   1,
					PageToken:  token,
				})
				if err != nil {
					t.Fatalf("list tasks with prefix %q: %v", tc.prefix, err)
				}
				for _, task := range page.Tasks {
					got = append(got, task.Id)
				}
				if page.NextPageToken == "" {
					break
				}
				token = page.NextPageToken
			}
			if len(got) != len(tc.want) {
				t.Fatalf("NamePrefix %q returned IDs %v, want %v", tc.prefix, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("NamePrefix %q returned IDs %v, want %v", tc.prefix, got, tc.want)
				}
			}
		})
	}
}
