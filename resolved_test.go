package dbunit

import (
	"context"
	"encoding/json"
	"github.com/viant/dbunit/fixture"
	"github.com/viant/toolbox/data"
	"testing"
)

func TestResolvedSourcesSurviveCompileAndSerialization(t *testing.T) {
	_, engine := testDB(t, `CREATE TABLE resolved(id INTEGER PRIMARY KEY,value TEXT)`)
	calls := 0
	state := data.Map{"Next": func(any, data.Map) (any, error) { calls++; return "wrong", nil }}
	source := fixture.Resolved([]map[string]any{{"id": 1, "value": "$Next(0)"}})
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var restored fixture.Source
	if err = json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	definition := Case{State: state, Prepare: []Dataset{{Table: Table("resolved"), Source: restored}}, Expect: []Expectation{{Table: Table("resolved"), Expected: source}}}
	plan, err := engine.Compile(context.Background(), definition)
	if err != nil {
		t.Fatal(err)
	}
	report, err := engine.RunPlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("%v %+v", err, report)
	}
	if calls != 0 {
		t.Fatalf("resolved source expanded %d times", calls)
	}
}
